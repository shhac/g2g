package land

import (
	"fmt"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/repair"
)

// Step is what land would do about one branch.
//
// A step is not a command. It is the decision, and the sequence a preview
// renders and the sequence Apply walks are both built from it, so the recipe
// shown and the work done cannot describe different things.
type Step struct {
	Branch string
	Number int
	URL    string

	// Base is where the pull request has to point for its merge to put this
	// branch's work into the trunk rather than into the branch below it.
	Base string
	// From is the base it points at now, set only when that is not Base.
	From string

	// Push is set when the remote does not already have this branch exactly
	// as it is here.
	Push bool
	// Admin is set when merging needs branch protection bypassed.
	Admin bool
	// Landed means the work is already in the base by content, so there is
	// nothing to merge and only the cleanup is left. It is answered by Git,
	// never by the pull request's own state.
	Landed bool
	// RemoteTip is what the remote held for this branch when the descent was
	// planned, and is empty when it held nothing.
	//
	// It is what tells this branch's own previous version from somebody else's
	// commit. Once a replay has rewritten a branch the remote legitimately
	// holds work the branch no longer has, which is indistinguishable from a
	// reviewer having pushed a fix -- push refuses both, correctly, and land
	// has to know which one it is looking at.
	RemoteTip string
}

// Retargets reports a pull request that has to be pointed somewhere else first.
func (s Step) Retargets() bool { return s.From != "" }

// Merges reports a step that still has a merge to perform.
func (s Step) Merges() bool { return !s.Landed }

// facts is everything known about one branch at the moment it is classified.
//
// It is a value rather than a set of arguments because classify is re-run per
// cycle against a world the previous cycle changed, and a caller that has to
// remember the argument order is a caller that will eventually pass the
// pre-merge answer to the post-merge question.
type facts struct {
	Step  githubstack.PathStep
	State githubstack.MergeState
	// Landed is Git's answer, by content, to whether this branch's work is
	// already in its base. A squash merge is invisible to the pull request's
	// head and plain to a tree comparison.
	Landed bool
	// Current is whether the remote already has this branch exactly as it is
	// here. False means a push comes first.
	Current bool
	// Tip is the commit this branch is on here. GitHub answers about the head
	// it currently knows, which for a while after a push is the one before it,
	// so a verdict is only about this branch when the two agree.
	Tip   string
	Admin bool
}

// classify decides what land does about one branch, or why it will not.
//
// A refusal carries structure rather than a sentence so the caller can render
// the reason and the ways out in whichever shape it needs; an empty note means
// the step is good to run.
func classify(in facts) (Step, repair.Note) {
	branch := in.Step.Branch
	step := Step{Branch: branch, Base: in.Step.ExpectedBase}

	// Git first, always. A squash merge lands the work under a head the branch
	// never had, so the pull request can read merged, closed or missing while
	// the work is plainly in the trunk -- and the advice for each of those is
	// to do something about a branch that is already finished.
	if in.Landed {
		step.Landed = true
		if open := in.Step.Resolution.Open; open != nil {
			step.Number, step.URL = open.Number, open.URL
		}
		return step, repair.Note{}
	}

	switch in.Step.Classify() {
	case githubstack.StepAmbiguous:
		return Step{}, repair.Note{
			Reason: fmt.Sprintf("%s has %d open pull requests, so which one to merge cannot be derived", branch, in.Step.Resolution.OpenCount),
			Ways: []repair.Step{
				{Effect: "close the ones that are not wanted, then rerun"},
			},
		}
	case githubstack.StepMissing:
		return Step{}, repair.Note{
			Reason: branch + " has no pull request to merge",
			Ways: []repair.Step{
				{Command: "g2g submit", Effect: "open one for it"},
			},
		}
	case githubstack.StepSuperseded:
		// Landing is handled above, by content, so reaching here means the
		// work is not upstream -- and the two ways that happens want different
		// sentences. A merged pull request with work left over is a branch
		// somebody committed to afterwards, which is not the same event as one
		// that was closed and abandoned, and telling them apart is the
		// difference between "you have unpublished work" and "you have none".
		if in.Step.Merged() {
			return Step{}, repair.Note{
				Reason: fmt.Sprintf("%s merged, but %s carries work that is not in %s", pullRequest(branch, in.Step.Resolution.Latest.Number), branch, in.Step.ExpectedBase),
				Ways: []repair.Step{
					{Command: "g2g submit", Effect: "open a pull request for what is left"},
				},
			}
		}
		return Step{}, repair.Note{
			Reason: branch + " has no open pull request; the last one was closed without merging",
			Ways: []repair.Step{
				{Command: "g2g submit", Effect: "open a replacement"},
			},
		}
	}

	open := in.Step.Resolution.Open
	step.Number, step.URL = open.Number, open.URL
	if open.Base != step.Base {
		step.From = open.Base
	}
	step.Push = !in.Current

	if in.State.Draft {
		return Step{}, repair.Note{
			Reason: fmt.Sprintf("%s is a draft", pullRequest(branch, open.Number)),
			Ways: []repair.Step{
				{Command: fmt.Sprintf("gh pr ready %d", open.Number), Effect: "mark it ready for review"},
			},
		}
	}
	// Only when GitHub is looking at the commit this branch is actually on.
	// A verdict about the previous head is a verdict about work that is being
	// replaced, and CONFLICTING is the dangerous one to believe: it is
	// plausible, it is actionable, and the action it invites -- go and rebase
	// -- is wrong. UNKNOWN at least admits to not knowing. The head is the
	// reliable signal and mergeability is derived from it, so the head is what
	// decides whether to read it at all.
	if in.State.Mergeable == githubstack.MergeableConflicting && in.State.HeadOID == in.Tip {
		return Step{}, repair.Note{
			Reason: fmt.Sprintf("%s conflicts with its base", pullRequest(branch, open.Number)),
			Ways: []repair.Step{
				{Command: "g2g sync", Effect: "bring the stack up to date and replay it"},
			},
		}
	}
	// Review and protection are asked separately even though a protected
	// repository reports an unapproved pull request as blocked. --admin
	// bypasses both, and someone reaching for it to get past restarted checks
	// should not silently also get past a review nobody gave.
	if requiresReview(in.State) && !in.Admin {
		return Step{}, repair.Note{
			Reason: fmt.Sprintf("%s is not approved (%s)", pullRequest(branch, open.Number), reviewWord(in.State.Review)),
			Ways: []repair.Step{
				{Effect: "get it approved"},
				{Command: "g2g land --admin", Effect: "merge without an approval"},
			},
		}
	}
	if in.State.StateStatus == githubstack.StatusBlocked {
		if !in.Admin {
			return Step{}, repair.Note{
				Reason: fmt.Sprintf("%s is blocked by branch protection, which is what required checks report while they run", pullRequest(branch, open.Number)),
				Ways: []repair.Step{
					{Command: "g2g land --admin", Effect: "merge without waiting for them"},
					{Effect: "wait for the checks and rerun"},
				},
			}
		}
		step.Admin = true
	}
	return step, repair.Note{}
}

// requiresReview reports a pull request whose repository asked for a review it
// has not had.
//
// An empty decision means the repository asks for no review at all, which is
// not the same as a review that has not happened yet and must not read as one.
func requiresReview(state githubstack.MergeState) bool {
	return state.Review != "" && state.Review != githubstack.ReviewApproved
}

func reviewWord(decision string) string {
	if decision == "CHANGES_REQUESTED" {
		return "changes requested"
	}
	return "review required"
}

func pullRequest(branch string, number int) string {
	return fmt.Sprintf("%s (#%d)", branch, number)
}

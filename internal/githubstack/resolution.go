package githubstack

// Resolution is the pull request that represents one branch, together with the
// facts a caller needs when no single open pull request exists.
//
// A branch's identity is its one open pull request. A long-lived stack
// accumulates closed and merged pull requests for a reused branch name, and
// those are history rather than ambiguity — treating any second match as
// ambiguous blocked branches whose only fault was having been submitted
// before. Only two or more simultaneously open pull requests are genuinely
// ambiguous, because then nothing distinguishes which one the stack means.
type Resolution struct {
	// Open is the single open pull request, or nil when there is not exactly one.
	Open *PullRequest
	// Latest is the highest-numbered pull request of any state. It reports the
	// closed or merged state of a branch that has no open pull request.
	Latest *PullRequest
	// OpenCount distinguishes "none open" from "too many open".
	OpenCount int
}

// Ambiguous reports the only case g2g refuses to interpret.
func (r Resolution) Ambiguous() bool { return r.OpenCount > 1 }

// Superseded reports a branch whose pull requests are all closed or merged.
// An ambiguous branch has open pull requests and is deliberately not included.
func (r Resolution) Superseded() bool { return r.OpenCount == 0 && r.Latest != nil }

// ResolveHeads applies the open-is-identity rule to every inspected branch.
func ResolveHeads(prs []PullRequest) map[string]Resolution {
	resolutions := make(map[string]Resolution, len(prs))
	for _, pr := range prs {
		resolution := resolutions[pr.Head]
		if resolution.Latest == nil || pr.Number > resolution.Latest.Number {
			latest := pr
			resolution.Latest = &latest
		}
		if pr.State == stateOpen {
			resolution.OpenCount++
			open := pr
			resolution.Open = &open
		}
		resolutions[pr.Head] = resolution
	}
	for head, resolution := range resolutions {
		if resolution.OpenCount != 1 {
			resolution.Open = nil
			resolutions[head] = resolution
		}
	}
	return resolutions
}

const stateOpen = "OPEN"

// PathStep is one branch on an ordered stack: the branch, the base it should
// sit on, and the pull request that represents it.
type PathStep struct {
	Branch       string
	ExpectedBase string
	Resolution   Resolution
}

// Across yields each branch with the base its parent edge names and its
// resolved pull request, for a selection that need not be a path.
//
// There used to be a rolling form beside it, Along, in which every branch sat
// on the one before it. A fork has no "branch before this one", so on a fork
// that compared a pull request against whichever sibling happened to sort
// first, and submit opened pull requests on siblings until it stopped using
// it. Every caller now names its edges: stack.Snapshot.Steps the recorded
// ones, land the trunk each branch is merged into. Deriving the chain in each
// command separately is also what once let a caller drift into scanning for
// the first open pull request instead of going through the open-is-identity
// rule, which is why the resolution is applied here and nowhere else.
//
// A branch whose parent is outside the selection yields an empty expected base.
// It is a root of the selection, and a root is not assessed against anything.
func Across(parents map[string]string, branches []string, prs []PullRequest) func(func(PathStep) bool) {
	resolutions := ResolveHeads(prs)
	return func(yield func(PathStep) bool) {
		for _, branch := range branches {
			if !yield(PathStep{Branch: branch, ExpectedBase: parents[branch], Resolution: resolutions[branch]}) {
				return
			}
		}
	}
}

// StepState is what one branch's pull request says about itself, once the
// open-is-identity rule and the rolling base have both been applied.
//
// The classification is here rather than in each caller because link and submit
// were each running the same four-way decision over Resolution and diverging
// only in what they did with the answer. Deriving the answer twice is what let
// the chain itself drift before Along existed; this is the same fix one level
// down.
type StepState int

const (
	// StepAligned is an open pull request already based where it belongs.
	StepAligned StepState = iota
	// StepAmbiguous is more than one open pull request for the branch, which
	// nothing here resolves automatically.
	StepAmbiguous
	// StepBaseMismatch is an open pull request based on the wrong branch.
	StepBaseMismatch
	// StepSuperseded is a closed or merged pull request and no open one.
	StepSuperseded
	// StepMissing is no pull request at all.
	StepMissing
)

// Classify answers what a step is. Callers apply their own policy to the
// answer: link treats StepMissing as blocking because it can only link what
// exists, and submit treats it as ordinary because it is about to create one.
func (s PathStep) Classify() StepState {
	switch {
	case s.Resolution.Ambiguous():
		return StepAmbiguous
	case s.Resolution.Open != nil:
		if s.Resolution.Open.Base != s.ExpectedBase {
			return StepBaseMismatch
		}
		return StepAligned
	case s.Resolution.Superseded():
		return StepSuperseded
	default:
		return StepMissing
	}
}

// Merged reports a superseded step whose latest pull request was merged rather
// than closed. It is the one distinction callers draw beyond the state itself.
func (s PathStep) Merged() bool {
	return s.Resolution.Superseded() && s.Resolution.Latest.State == stateMerged
}

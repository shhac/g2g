// Package link plans and applies a Graphite-authoritative GitHub stack link.
package link

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/graphite"
	"github.com/shhac/g2g/internal/stack"
)

// Git provides the read-only local repository facts needed for a plan.
type Git interface {
	stack.Git
	Clean(context.Context) error
}

// Graphite discovers Graphite's declared ancestry without a checkout.
type Graphite interface {
	stack.Graphite
	Discover(context.Context, string) (graphite.Stack, error)
	TrackedBranches(context.Context) ([]string, error)
}

// GitHub inspects existing PRs and performs the sole mutation.
type GitHub interface {
	Inspect(context.Context, []string) ([]githubstack.PullRequest, error)
	Link(context.Context, string, []string) error
}

// Tips compares a branch with the commit its pull request is on.
//
// It is optional. Without it currency is simply not reported, which is what
// every reader saw before: a pull request described as aligned while it did not
// contain the work sitting in the branch, because alignment is a statement
// about the base and never about the contents.
type Tips interface {
	Resolve(ctx context.Context, revision string) (string, error)
	// Cherry compares by content. Counting commit ids answered this question
	// wrongly in the ordinary case: a branch replayed onto a newer base carries
	// the same work under new ids, so every commit the base had gained was
	// reported as work of the reader's own — 1532 of them on one real stack,
	// none of which were theirs.
	Cherry(ctx context.Context, upstream, head, limit string) (absent, present []string, err error)
	// Absorbed answers the same question of a whole branch at once, which is
	// what a squash merge needs: it combines the branch's commits into one, so
	// that commit is equivalent to none of them and Cherry marks every one as
	// new while the branch as a whole contributes nothing.
	Absorbed(ctx context.Context, base, branch string) (bool, error)
}

// Service coordinates a safe link plan.
type Service struct {
	Git Git
	// Selector supplies the ordered path, from whichever source describes the
	// branch. link only needs a path, so it works with any of them.
	Selector stack.PathSelector
	GitHub   GitHub
	// Tips answers whether each pull request is on the branch's current commit.
	Tips Tips
}

// Currency is how a branch stands against the commit its pull request is on.
type Currency struct {
	// Unpushed is how many of this branch's own commits have no equivalent on
	// its pull request. It counts by content and stops at the branch's parent,
	// because a stacked branch's work is what sits above the branch below it —
	// not everything above the trunk, which is what made the count grow by the
	// size of the trunk every time somebody replayed onto it.
	Unpushed int
	// Diverged means the pull request carries work, by content, that this
	// branch does not: somebody pushed to it, or commits were dropped here.
	Diverged bool
	// Rewritten means the pull request has every one of this branch's commits
	// by content, but not as these commits — it was replayed or amended since
	// it was last pushed, so the pull request shows an older rendering of the
	// same work.
	//
	// This is the state a restacked stack is in, and it used to report as a
	// divergence with the trunk's commits counted as the reader's own. Nothing
	// is missing from it; it needs pushing.
	Rewritten bool
}

// Current reports a pull request that already has everything the branch does,
// as the commits the branch has.
func (c Currency) Current() bool { return c.Unpushed == 0 && !c.Diverged && !c.Rewritten }

// Plan is the validated, printable bottom-to-top linking action: the shared
// discovery every command performs, plus link's own policy verdict on it.
type Plan struct {
	stack.Discovery
	Issues []Issue
	// Currency says, per branch, whether its pull request is on the commit the
	// branch is on. Absent when no Tips reader was configured.
	Currency map[string]Currency
}

// IssueKind classifies why a node blocks apply. link's policy is stricter than
// sync's, and a caller needs to distinguish them to say which command fixes
// what: sync reconciles a wrong base but deliberately refuses to invent,
// disambiguate, or reopen a pull request.
type IssueKind string

const (
	// IssueBase is a pull request that exists and is open but is not based on
	// its Graphite predecessor. This is the one kind sync repairs.
	IssueBase IssueKind = "base"
	// IssueMissing is a branch with no open pull request.
	IssueMissing IssueKind = "missing"
	// IssueClosed is a branch whose pull requests were closed without merging.
	// A replacement can be created, so submit resolves it.
	IssueClosed IssueKind = "closed"
	// IssueMerged is a branch whose work has landed. Nothing g2g does fixes
	// this: the branch no longer belongs in the stack, and only Graphite can
	// restack around it.
	IssueMerged IssueKind = "merged"
	// IssueAmbiguous is a branch with more than one open pull request.
	IssueAmbiguous IssueKind = "ambiguous"
	// IssueLanded is a branch with no pull request to project whose work is
	// already in the branch below it, by content.
	//
	// It is told apart from IssueMissing and IssueClosed because the advice for
	// those is to open a pull request, and opening one for work already in the
	// trunk sends somebody to submit an empty change. GitHub cannot answer
	// this: a squash merge lands the work under a pull request with a different
	// head, and a series cherry-picked by somebody else has no pull request at
	// all.
	IssueLanded IssueKind = "landed"
)

// Issue is a safe, actionable reason a displayed path node cannot be applied.
type Issue struct {
	Branch string
	Reason string
	Kind   IssueKind
	// Number is the pull request the issue is about, zero when there is none.
	// A closed one is the case worth carrying: "submit will open a new PR" is
	// advice a person can only judge by going and reading why the old one was
	// closed, and they need its number to do that.
	Number int
}

// MergedBranches lists branches whose pull requests have landed. They are
// reported first, because no g2g command resolves them — the stack itself is
// stale and Graphite has to restack around them.
func (p Plan) MergedBranches() []string {
	var merged []string
	for _, issue := range p.Issues {
		if issue.Kind == IssueMerged {
			merged = append(merged, issue.Branch)
		}
	}
	return merged
}

// LandedBranches lists branches whose work is already in the branch below them.
// Like a merged pull request, no g2g projection fixes them: what is left is a
// branch that no longer belongs in the stack.
func (p Plan) LandedBranches() []string {
	var landed []string
	for _, issue := range p.Issues {
		if issue.Kind == IssueLanded {
			landed = append(landed, issue.Branch)
		}
	}
	return landed
}

// SyncRepairable reports whether every blocker is a base that sync is designed
// to reconcile, so a caller can name the command that actually fixes this
// instead of leaving the user to work it out.
func (p Plan) SyncRepairable() bool { return p.allIssuesAre(IssueBase) }

// SubmitRepairable reports whether every blocker is a branch submit can
// resolve: one with no pull request, or one whose pull request was closed
// without merging, for which submit creates a replacement.
func (p Plan) SubmitRepairable() bool { return p.allIssuesAre(IssueMissing, IssueClosed) }

func (p Plan) allIssuesAre(kinds ...IssueKind) bool {
	if len(p.Issues) == 0 {
		return false
	}
	for _, issue := range p.Issues {
		if !slices.Contains(kinds, issue.Kind) {
			return false
		}
	}
	return true
}

// NothingToLink reports whether the fully validated path is shorter than the
// minimum GitHub stack link accepts. Unresolved PR state is never a no-op.
func (p Plan) NothingToLink() bool {
	return len(p.Issues) == 0 && len(p.Branches) < 2
}

// Selection captures every no-checkout path selector shared by link, sync,
// and the Git-only push escape hatch.
type Selection = stack.Selection

// DiscoverWithOptions resolves an optional pivot and optional full linear
// stack without checking out any branch.
func (s Service) DiscoverWithOptions(ctx context.Context, selection Selection) (Plan, error) {
	if s.Git == nil || s.Selector == nil || s.GitHub == nil {
		return Plan{}, fmt.Errorf("link service is not fully configured")
	}
	discovery, err := stack.Discover(ctx, s.Selector, s.GitHub, selection, "gh stack link")
	if err != nil {
		return Plan{}, err
	}
	return Plan{Discovery: discovery}, nil
}

// Plan applies link's stricter policy: existing pull requests must already
// have the expected base relationship. sync deliberately has a separate,
// explicit reconciliation policy for detected divergence.
func (s Service) Plan(ctx context.Context, selection Selection) (Plan, error) {
	plan, err := s.DiscoverWithOptions(ctx, selection)
	if err != nil {
		return Plan{}, err
	}
	plan.Issues = assessPRs(plan.PullRequests, plan.Base, plan.Branches, plan.Parents)
	if err := s.markLanded(ctx, plan); err != nil {
		return Plan{}, err
	}
	plan.Currency, err = s.currency(ctx, plan)
	if err != nil {
		return Plan{}, err
	}
	if len(plan.Issues) != 0 {
		diagnostic.Event(ctx, "link.plan", diagnostic.Field{Key: "decision", Value: "blocked"}, diagnostic.Field{Key: "reasons", Value: issueSummary(plan.Issues)})
	} else if plan.NothingToLink() {
		diagnostic.Event(ctx, "link.plan", diagnostic.Field{Key: "decision", Value: "no_op"}, diagnostic.Field{Key: "reason", Value: "fewer_than_two_pr_branches"})
	} else {
		diagnostic.Event(ctx, "link.plan", diagnostic.Field{Key: "decision", Value: "ready"}, diagnostic.Field{Key: "branches", Value: strings.Join(plan.Branches, ",")})
	}
	return plan, nil
}

// Revalidate repeats all discovery and local state checks immediately before
// a mutation, and refuses if the result differs from the preview the caller
// already rendered. Callers run Execute themselves: the CLI interposes the
// ready-to-apply render and its flush between the two, so composing them here
// would describe a sequence production never performs.
func (s Service) Revalidate(ctx context.Context, selection Selection, preview Plan) (Plan, error) {
	if s.Git == nil || s.Selector == nil || s.GitHub == nil {
		return Plan{}, fmt.Errorf("link service is not fully configured")
	}
	if err := s.Git.Clean(ctx); err != nil {
		return Plan{}, err
	}
	plan, err := s.Plan(ctx, selection)
	if err != nil {
		return Plan{}, err
	}
	if err := diagnostic.Revalidated(ctx, "link", "link plan", plan.Equal(preview)); err != nil {
		return Plan{}, err
	}
	if len(plan.Issues) != 0 {
		return Plan{}, fmt.Errorf("link preview has unresolved GitHub PR mappings; fix them and rerun before --apply")
	}
	return plan, nil
}

// Execute invokes the sole GitHub mutation for a revalidated, apply-eligible
// plan. It does not rediscover or render anything.
func (s Service) Execute(ctx context.Context, plan Plan) error {
	if s.GitHub == nil {
		return fmt.Errorf("link service is not fully configured")
	}
	if err := plan.Snapshot.RequireActionable("g2g link"); err != nil {
		return err
	}
	if len(plan.Issues) != 0 {
		return fmt.Errorf("link preview has unresolved GitHub PR mappings; fix them and rerun before --apply")
	}
	if plan.NothingToLink() {
		diagnostic.Event(ctx, "link.apply", diagnostic.Field{Key: "decision", Value: "skipped"}, diagnostic.Field{Key: "reason", Value: "fewer_than_two_pr_branches"})
		return nil
	}
	diagnostic.Event(ctx, "link.apply", diagnostic.Field{Key: "decision", Value: "run"})
	return s.GitHub.Link(ctx, plan.Base, plan.Branches)
}

// markLanded re-reads the branches whose only problem is that they have no
// pull request to project, and says so differently where the work is already
// below them.
//
// Only those are asked, which is what bounds the cost: a branch with an open
// pull request has somewhere for its work to be, and one whose pull request
// merged has already been answered by GitHub. Two local reads each, and only
// for the branches where the answer changes what to do.
func (s Service) markLanded(ctx context.Context, plan Plan) error {
	if s.Tips == nil {
		return nil
	}
	asking := make([]string, len(plan.Issues))
	for index, issue := range plan.Issues {
		if issue.Kind == IssueMissing || issue.Kind == IssueClosed {
			asking[index] = issue.Branch
		}
	}
	// Distinct elements of a slice that already exists, so the writes need no
	// lock: each read owns the one issue it was given.
	return eachBranch(ctx, asking, func(ctx context.Context, index int, branch string) error {
		if branch == "" {
			return nil
		}
		landed, err := s.landed(ctx, plan, branch)
		if err != nil {
			return err
		}
		if !landed {
			return nil
		}
		below := ownCommitsFrom(plan, branch)
		plan.Issues[index] = Issue{
			Branch: branch,
			Kind:   IssueLanded,
			Number: plan.Issues[index].Number,
			Reason: "landed in " + below,
		}
		return nil
	})
}

// landed reports a branch with nothing left to contribute to the branch below
// it. The cheap per-commit question comes first; the whole-branch merge is
// asked only of what it says no to, because that is the squash-merge case and
// it is the more expensive read.
func (s Service) landed(ctx context.Context, plan Plan, branch string) (bool, error) {
	below := ownCommitsFrom(plan, branch)
	absent, _, err := s.Tips.Cherry(ctx, below, branch, below)
	if err != nil {
		// A branch sharing no history with the one below it cannot be
		// compared, which is an answer rather than a failure.
		return false, nil
	}
	if len(absent) == 0 {
		return true, nil
	}
	absorbed, err := s.Tips.Absorbed(ctx, below, branch)
	if err != nil {
		return false, nil
	}
	return absorbed, nil
}

func issueSummary(issues []Issue) string {
	parts := make([]string, len(issues))
	for index, issue := range issues {
		parts[index] = issue.Branch + ": " + issue.Reason
	}
	return strings.Join(parts, "; ")
}

// Equal compares every fact that can affect the command shown in a preview or
// the GitHub action performed after revalidation.
func (left Plan) Equal(right Plan) bool {
	return left.Discovery.Equal(right.Discovery) &&
		slices.Equal(left.Issues, right.Issues) &&
		maps.Equal(left.Currency, right.Currency)
}

func assessPRs(prs []githubstack.PullRequest, baseBranch string, branches []string, parents map[string]string) []Issue {
	issues := make([]Issue, 0)
	for step := range steps(prs, baseBranch, branches, parents) {
		// link can only project what exists, so a missing pull request blocks
		// here where it would be ordinary for submit.
		switch step.Classify() {
		case githubstack.StepAligned:
		case githubstack.StepAmbiguous:
			issues = append(issues, Issue{Branch: step.Branch, Kind: IssueAmbiguous, Reason: fmt.Sprintf("%d open PRs", step.Resolution.OpenCount)})
		case githubstack.StepBaseMismatch:
			issues = append(issues, Issue{Branch: step.Branch, Kind: IssueBase, Number: step.Resolution.Open.Number, Reason: fmt.Sprintf("PR #%d base %s, want %s", step.Resolution.Open.Number, step.Resolution.Open.Base, step.ExpectedBase)})
		case githubstack.StepSuperseded:
			kind := IssueClosed
			if step.Merged() {
				kind = IssueMerged
			}
			issues = append(issues, Issue{Branch: step.Branch, Kind: kind, Number: step.Resolution.Latest.Number, Reason: "PR " + strings.ToLower(step.Resolution.Latest.State)})
		default:
			issues = append(issues, Issue{Branch: step.Branch, Kind: IssueMissing, Reason: "no open PR"})
		}
	}
	return issues
}

// steps walks the selection the way its shape demands. A path rolls its base;
// a forked selection takes each branch's base from its recorded parent, because
// "the branch before this one" stops meaning anything once there are siblings.
func steps(prs []githubstack.PullRequest, baseBranch string, branches []string, parents map[string]string) func(func(githubstack.PathStep) bool) {
	if len(parents) != 0 {
		return githubstack.Across(parents, branches, prs)
	}
	return githubstack.Along(baseBranch, branches, prs)
}

// currency compares each branch with the commit its open pull request is on.
//
// A pull request whose head this repository does not have is reported as
// diverged rather than counted: the commits are not here to count, and
// fetching them to say how many would turn a read into a network write.
func (s Service) currency(ctx context.Context, plan Plan) (map[string]Currency, error) {
	if s.Tips == nil {
		return nil, nil
	}
	open := map[string]githubstack.PullRequest{}
	for branch, resolution := range githubstack.ResolveHeads(plan.PullRequests) {
		if resolution.Open != nil {
			open[branch] = *resolution.Open
		}
	}
	// Each branch is several git processes and none of them needs another
	// branch's answer, so they are asked at once rather than in turn. The
	// results land in a slice sized first, which is what makes that safe
	// without a lock.
	states := make([]*Currency, len(plan.Branches))
	err := eachBranch(ctx, plan.Branches, func(ctx context.Context, index int, branch string) error {
		pr, published := open[branch]
		if !published || pr.HeadOID == "" {
			return nil
		}
		state, err := s.compareWithPullRequest(ctx, plan, branch, pr.HeadOID)
		if err != nil {
			return err
		}
		states[index] = &state
		return nil
	})
	if err != nil {
		return nil, err
	}
	currency := make(map[string]Currency, len(plan.Branches))
	for index, state := range states {
		if state != nil {
			currency[plan.Branches[index]] = *state
		}
	}
	return currency, nil
}

// compareWithPullRequest is one branch's whole answer, and the unit that runs
// alongside the others.
func (s Service) compareWithPullRequest(ctx context.Context, plan Plan, branch, head string) (Currency, error) {
	local, err := s.Tips.Resolve(ctx, branch)
	if err != nil {
		return Currency{}, err
	}
	if local == head {
		return Currency{}, nil
	}
	if _, err := s.Tips.Resolve(ctx, head); err != nil {
		// On a commit this repository has never seen, so there is nothing here
		// to compare it with.
		return Currency{Diverged: true}, nil
	}
	return s.compare(ctx, plan, branch, head)
}

// compare asks what each side holds that the other does not, by content.
//
// The two calls are not symmetric and cannot be. This branch's side is limited
// to its own commits, because everything below them belongs to the branch it
// is stacked on. The pull request's side needs no limit: branch..head already
// excludes everything the branch can reach, so what is left is the pull
// request's own commits and never the base's.
func (s Service) compare(ctx context.Context, plan Plan, branch, head string) (Currency, error) {
	ours, _, err := s.Tips.Cherry(ctx, head, branch, ownCommitsFrom(plan, branch))
	if err != nil {
		return Currency{}, err
	}
	diverged, err := s.diverged(ctx, branch, head)
	if err != nil {
		return Currency{}, err
	}
	return Currency{Unpushed: len(ours), Diverged: diverged, Rewritten: len(ours) == 0 && !diverged}, nil
}

// diverged reports a pull request carrying content this branch does not.
//
// The whole-branch merge is asked first because the per-commit comparison
// cannot be bounded on this side: it has to compute a patch id for every commit
// the branch holds that the pull request does not, which on a stack sitting on
// a busy trunk is the whole trunk. Measured on a synthetic 1500-commit trunk it
// was 0.20s a branch against 0.03s, and it was most of what a status spent.
//
// A branch that absorbs into the pull request's commit without changing it has
// nothing to be diverged about, and that is the common answer. Only where it
// says otherwise is the expensive question asked — which is also what keeps a
// Git too old for merge-tree correct rather than merely fast, since such a Git
// answers "not absorbed" to everything and would otherwise report every
// replayed branch as diverged.
func (s Service) diverged(ctx context.Context, branch, head string) (bool, error) {
	absorbed, err := s.Tips.Absorbed(ctx, branch, head)
	if err != nil {
		return false, err
	}
	if absorbed {
		return false, nil
	}
	theirs, _, err := s.Tips.Cherry(ctx, branch, head, "")
	if err != nil {
		return false, err
	}
	return len(theirs) != 0, nil
}

// ownCommitsFrom is where this branch's own work starts: the branch below it in
// the selection, or the base when nothing is.
func ownCommitsFrom(plan Plan, branch string) string {
	if parent, within := plan.ParentOf(branch); within {
		return parent
	}
	return plan.Base
}

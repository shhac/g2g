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
	Divergence(ctx context.Context, other, target string) (ahead, behind int, err error)
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
	// Unpushed is how many commits the branch has that its pull request does
	// not. Diverged means the pull request is on a commit this branch does not
	// contain at all, so there is no count to give.
	Unpushed int
	Diverged bool
}

// Current reports a pull request that already has everything the branch does.
func (c Currency) Current() bool { return c.Unpushed == 0 && !c.Diverged }

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
	currency := make(map[string]Currency, len(plan.Branches))
	for _, branch := range plan.Branches {
		pr, published := open[branch]
		if !published || pr.HeadOID == "" {
			continue
		}
		local, err := s.Tips.Resolve(ctx, branch)
		if err != nil {
			return nil, err
		}
		if local == pr.HeadOID {
			currency[branch] = Currency{}
			continue
		}
		if _, err := s.Tips.Resolve(ctx, pr.HeadOID); err != nil {
			currency[branch] = Currency{Diverged: true}
			continue
		}
		theirs, ours, err := s.Tips.Divergence(ctx, pr.HeadOID, branch)
		if err != nil {
			return nil, err
		}
		if theirs > 0 {
			currency[branch] = Currency{Unpushed: ours, Diverged: true}
			continue
		}
		currency[branch] = Currency{Unpushed: ours}
	}
	return currency, nil
}

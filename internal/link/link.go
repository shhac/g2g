// Package link plans and applies a Graphite-authoritative GitHub stack link.
package link

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/shhac/gt2gh/internal/diagnostic"
	"github.com/shhac/gt2gh/internal/githubstack"
	"github.com/shhac/gt2gh/internal/graphite"
	"github.com/shhac/gt2gh/internal/stack"
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

// Service coordinates a safe link plan.
type Service struct {
	Git Git
	// Selector supplies the ordered path, from whichever source describes the
	// branch. link only needs a path, so it works with any of them.
	Selector stack.PathSelector
	GitHub   GitHub
}

// Plan is the validated, printable bottom-to-top linking action: the shared
// discovery every command performs, plus link's own policy verdict on it.
type Plan struct {
	stack.Discovery
	Issues []Issue
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
	// IssueMerged is a branch whose work has landed. Nothing gt2gh does fixes
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
}

// MergedBranches lists branches whose pull requests have landed. They are
// reported first, because no gt2gh command resolves them — the stack itself is
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
	plan.Issues = assessPRs(plan.PullRequests, plan.Base, plan.Branches)
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
	return left.Discovery.Equal(right.Discovery) && slices.Equal(left.Issues, right.Issues)
}

func assessPRs(prs []githubstack.PullRequest, baseBranch string, branches []string) []Issue {
	issues := make([]Issue, 0)
	for step := range githubstack.Along(baseBranch, branches, prs) {
		resolution := step.Resolution
		switch {
		case resolution.Ambiguous():
			issues = append(issues, Issue{Branch: step.Branch, Kind: IssueAmbiguous, Reason: fmt.Sprintf("%d open pull requests", resolution.OpenCount)})
		case resolution.Open != nil:
			if resolution.Open.Base != step.ExpectedBase {
				issues = append(issues, Issue{Branch: step.Branch, Kind: IssueBase, Reason: fmt.Sprintf("PR #%d base %s, want %s", resolution.Open.Number, resolution.Open.Base, step.ExpectedBase)})
			}
		case resolution.Superseded():
			kind := IssueClosed
			if resolution.Latest.State == "MERGED" {
				kind = IssueMerged
			}
			issues = append(issues, Issue{Branch: step.Branch, Kind: kind, Reason: strings.ToLower(resolution.Latest.State) + " pull request"})
		default:
			issues = append(issues, Issue{Branch: step.Branch, Kind: IssueMissing, Reason: "no open pull request"})
		}
	}
	return issues
}

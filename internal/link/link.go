// Package link plans and applies a GitHub stack link for a selected path, from
// whichever source describes it.
package link

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/stack"
)

// Git provides the read-only local repository facts needed for a plan.
type Git interface {
	stack.Git
	Clean(context.Context) error
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
	// Tips answers whether each pull request is on the branch's current commit.
	Tips Tips
}

// Plan is the validated, printable bottom-to-top linking action: the shared
// discovery every command performs, plus link's own policy verdict on it.
type Plan struct {
	stack.Discovery
	Issues []Issue
	// Currency says, per branch, whether its pull request is on the commit the
	// branch is on. Absent when no Tips reader was configured.
	Currency map[string]Currency
}

// IssueKind classifies why a node blocks apply. Each kind has a different
// command that repairs it, and Repair is where that is decided.
type IssueKind string

const (
	// IssueBase is a pull request that exists and is open but is not based on
	// the branch below it. retarget repairs it.
	IssueBase IssueKind = "base"
	// IssueMissing is a branch with no open pull request.
	IssueMissing IssueKind = "missing"
	// IssueClosed is a branch whose pull requests were closed without merging.
	// A replacement can be created, so submit resolves it.
	IssueClosed IssueKind = "closed"
	// IssueMerged is a branch whose pull request merged. The branch no longer
	// belongs in the stack, and what brings the stack past it depends on which
	// record describes it.
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

// MergedBranches lists branches whose pull requests have merged. They are
// reported first: the stack itself is stale, and nothing else is worth doing
// until it is brought past them.
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

// Ready reports a service with everything it needs.
//
// One rule, called by both the guard below and the command registration in
// internal/cli. They were two hand-written conjunctions before, and three of
// them had already drifted -- a command could be registered and then refuse on
// use, or be hidden from a build that could have run it.
func (s Service) Ready() bool {
	return s.Git != nil && s.Selector != nil && s.GitHub != nil
}

// discover resolves an optional pivot and optional full linear
// stack without checking out any branch.
func (s Service) discover(ctx context.Context, selection Selection) (Plan, error) {
	if !s.Ready() {
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
	plan, err := s.discover(ctx, selection)
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
	if err := plan.Snapshot.RequireActionable("g2g github link"); err != nil {
		return err
	}
	// gh stack link takes one ordered list. Handed a fork, it would link the
	// siblings as though each sat on the one before it.
	if err := plan.Snapshot.RequireLinear("link"); err != nil {
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

// ownCommitsFrom is where this branch's own work starts: the branch below it in
// the selection, or the base when nothing is.
func ownCommitsFrom(plan Plan, branch string) string {
	if parent, within := plan.ParentOf(branch); within {
		return parent
	}
	return plan.Base
}

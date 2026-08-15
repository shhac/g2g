// Package link plans and applies a Graphite-authoritative GitHub stack link.
package link

import (
	"context"
	"fmt"
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
	Git      Git
	Graphite Graphite
	GitHub   GitHub
}

// Plan is the validated, printable bottom-to-top linking action.
type Plan struct {
	Target       string
	TargetSource string
	GraphitePath []string
	Base         string
	BaseSource   string
	Branches     []string
	PullRequests []githubstack.PullRequest
	Issues       []Issue
}

// Issue is a safe, actionable reason a displayed path node cannot be applied.
type Issue struct{ Branch, Reason string }

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
	if s.Git == nil || s.Graphite == nil || s.GitHub == nil {
		return Plan{}, fmt.Errorf("link service is not fully configured")
	}
	snapshot, err := stack.Resolve(ctx, s.Git, s.Graphite, selection, "gh stack link")
	if err != nil {
		return Plan{}, err
	}
	diagnostic.Event(ctx, "link.target", diagnostic.Field{Key: "target", Value: snapshot.Target}, diagnostic.Field{Key: "source", Value: snapshot.TargetSource})
	diagnostic.Event(ctx, "link.trunk", diagnostic.Field{Key: "trunk", Value: snapshot.Base}, diagnostic.Field{Key: "source", Value: snapshot.BaseSource}, diagnostic.Field{Key: "path_branches", Value: strings.Join(snapshot.Branches, ",")})
	prs, err := s.GitHub.Inspect(ctx, snapshot.Branches)
	if err != nil {
		return Plan{}, err
	}
	diagnostic.Event(ctx, "github.native_stack_membership", diagnostic.Field{Key: "observation", Value: "per_pull_request"})
	return Plan{
		Target:       snapshot.Target,
		TargetSource: snapshot.TargetSource,
		GraphitePath: snapshot.GraphitePath,
		Base:         snapshot.Base,
		BaseSource:   snapshot.BaseSource,
		Branches:     snapshot.Branches,
		PullRequests: prs,
	}, nil
}

// Plan applies link's stricter policy: existing pull requests must already
// have the expected base relationship. sync deliberately has a separate,
// explicit reconciliation policy for detected divergence.
func (s Service) Plan(ctx context.Context, requestedBranch string) (Plan, error) {
	return s.PlanWithOptions(ctx, Selection{Branch: requestedBranch})
}

func (s Service) PlanWithOptions(ctx context.Context, selection Selection) (Plan, error) {
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

// Apply revalidates all discovery and local state immediately before invoking
// gh, and refuses if the revalidated plan differs from the preview.
func (s Service) Apply(ctx context.Context, requestedBranch string, preview Plan) (Plan, error) {
	plan, err := s.RevalidateWithOptions(ctx, Selection{Branch: requestedBranch}, preview)
	if err != nil {
		return Plan{}, err
	}
	if err := s.Execute(ctx, plan); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

func (s Service) RevalidateWithOptions(ctx context.Context, selection Selection, preview Plan) (Plan, error) {
	if s.Git == nil || s.Graphite == nil || s.GitHub == nil {
		return Plan{}, fmt.Errorf("link service is not fully configured")
	}
	if err := s.Git.Clean(ctx); err != nil {
		return Plan{}, err
	}
	plan, err := s.PlanWithOptions(ctx, selection)
	if err != nil {
		return Plan{}, err
	}
	if !plan.Equal(preview) {
		diagnostic.Event(ctx, "link.revalidation", diagnostic.Field{Key: "match", Value: "false"})
		return Plan{}, fmt.Errorf("link plan changed during revalidation; rerun without --apply to review the new plan")
	}
	diagnostic.Event(ctx, "link.revalidation", diagnostic.Field{Key: "match", Value: "true"})
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
	if left.Target != right.Target || left.TargetSource != right.TargetSource || left.Base != right.Base || left.BaseSource != right.BaseSource || !sameStrings(left.GraphitePath, right.GraphitePath) || !sameStrings(left.Branches, right.Branches) || len(left.PullRequests) != len(right.PullRequests) || len(left.Issues) != len(right.Issues) {
		return false
	}
	for index := range left.Issues {
		if left.Issues[index] != right.Issues[index] {
			return false
		}
	}
	for index := range left.PullRequests {
		if left.PullRequests[index] != right.PullRequests[index] {
			return false
		}
	}
	return true
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func assessPRs(prs []githubstack.PullRequest, baseBranch string, branches []string) []Issue {
	expectedBases := make(map[string]string, len(branches))
	base := baseBranch
	for _, branch := range branches {
		expectedBases[branch] = base
		base = branch
	}
	resolutions := githubstack.ResolveHeads(prs)
	issues := make([]Issue, 0)
	for _, branch := range branches {
		resolution := resolutions[branch]
		switch {
		case resolution.Ambiguous():
			issues = append(issues, Issue{Branch: branch, Reason: fmt.Sprintf("%d open pull requests", resolution.OpenCount)})
		case resolution.Open != nil:
			if expected := expectedBases[branch]; resolution.Open.Base != expected {
				issues = append(issues, Issue{Branch: branch, Reason: fmt.Sprintf("PR #%d base %s, want %s", resolution.Open.Number, resolution.Open.Base, expected)})
			}
		case resolution.Superseded():
			issues = append(issues, Issue{Branch: branch, Reason: strings.ToLower(resolution.Latest.State) + " pull request"})
		default:
			issues = append(issues, Issue{Branch: branch, Reason: "no open pull request"})
		}
	}
	return issues
}

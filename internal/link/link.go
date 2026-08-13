// Package link plans and applies a Graphite-authoritative GitHub stack link.
package link

import (
	"context"
	"fmt"
	"sort"
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

// Discover resolves a Graphite path and read-only GitHub PR facts without
// deciding whether their current native-stack relationship is safe to link.
// sync uses this to report divergence before choosing its own apply policy.
func (s Service) Discover(ctx context.Context, requestedBranch string) (Plan, error) {
	return s.DiscoverWithTrunk(ctx, requestedBranch, "")
}

// DiscoverWithTrunk resolves a target without checkout. requestedTrunk is an
// optional explicit Graphite-trunk override for a valid ancestry candidate.
func (s Service) DiscoverWithTrunk(ctx context.Context, requestedBranch, requestedTrunk string) (Plan, error) {
	return s.DiscoverWithOptions(ctx, Selection{Branch: requestedBranch, Trunk: requestedTrunk})
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
	diagnostic.Event(ctx, "github.native_stack_membership", diagnostic.Field{Key: "observation", Value: "not_observed"})
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
	return s.PlanWithTrunk(ctx, requestedBranch, "")
}

// PlanWithTrunk applies link's PR-safety checks after resolving an optional
// Graphite trunk override.
func (s Service) PlanWithTrunk(ctx context.Context, requestedBranch, requestedTrunk string) (Plan, error) {
	return s.PlanWithOptions(ctx, Selection{Branch: requestedBranch, Trunk: requestedTrunk})
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

func branchSet(branches []string) map[string]bool {
	set := make(map[string]bool, len(branches))
	for _, branch := range branches {
		set[branch] = true
	}
	return set
}

// selectBoundary chooses only among Graphite-declared trunk candidates on the
// selected ancestry. It never guesses by branch name. An explicit override is
// accepted only for one of those candidates; otherwise exactly one candidate
// is required.
// SelectBoundary chooses a declared trunk only from the selected ancestry.
// It is shared with the Git-only push command to preserve the same Graphite
// authority and multi-trunk safety rules.
func SelectBoundary(path, trunks []string, requestedTrunk string) (string, string, []string, error) {
	return stack.SelectBoundary(path, trunks, requestedTrunk)
}

func selectBoundary(path, trunks []string, requestedTrunk string) (string, string, []string, error) {
	return SelectBoundary(path, trunks, requestedTrunk)
}

// Apply revalidates all discovery and local state immediately before invoking
// gh, and refuses if the revalidated plan differs from the preview.
func (s Service) Apply(ctx context.Context, requestedBranch string, preview Plan) (Plan, error) {
	return s.ApplyWithTrunk(ctx, requestedBranch, "", preview)
}

func (s Service) ApplyWithTrunk(ctx context.Context, requestedBranch, requestedTrunk string, preview Plan) (Plan, error) {
	plan, err := s.RevalidateWithTrunk(ctx, requestedBranch, requestedTrunk, preview)
	if err != nil {
		return Plan{}, err
	}
	if err := s.Execute(ctx, plan); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

// RevalidateWithTrunk checks the exact preview immediately before mutation.
// Callers may render the returned plan only after this succeeds.
func (s Service) RevalidateWithTrunk(ctx context.Context, requestedBranch, requestedTrunk string, preview Plan) (Plan, error) {
	return s.RevalidateWithOptions(ctx, Selection{Branch: requestedBranch, Trunk: requestedTrunk}, preview)
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

// BranchCompletions returns only locally-present Graphite branches, sorted for
// deterministic Cobra completion. It does not inspect or change checkout.
func (s Service) BranchCompletions(ctx context.Context, prefix string) ([]string, error) {
	if s.Git == nil || s.Graphite == nil {
		return nil, fmt.Errorf("link service is not fully configured")
	}
	local, err := s.Git.LocalBranches(ctx)
	if err != nil {
		return nil, err
	}
	tracked, err := s.Graphite.TrackedBranches(ctx)
	if err != nil {
		return nil, err
	}
	available := make(map[string]bool, len(local))
	for _, branch := range local {
		available[branch] = true
	}
	var matches []string
	for _, branch := range tracked {
		if available[branch] && strings.HasPrefix(branch, prefix) {
			matches = append(matches, branch)
		}
	}
	sort.Strings(matches)
	return matches, nil
}

// TrunkCompletions derives deterministic, local Graphite trunk candidates
// from a no-checkout discovery pass.
func (s Service) TrunkCompletions(ctx context.Context, target, prefix string) ([]string, error) {
	if s.Git == nil || s.Graphite == nil {
		return nil, fmt.Errorf("link service is not fully configured")
	}
	local, err := s.Git.LocalBranches(ctx)
	if err != nil {
		return nil, err
	}
	if target == "" {
		var err error
		target, err = s.Git.CurrentBranch(ctx)
		if err != nil {
			return nil, err
		}
	}
	stack, err := s.Graphite.Discover(ctx, target)
	if err != nil {
		return nil, err
	}
	available := branchSet(local)
	var matches []string
	for _, trunk := range stack.Trunks {
		if available[trunk] && strings.HasPrefix(trunk, prefix) {
			matches = append(matches, trunk)
		}
	}
	sort.Strings(matches)
	return matches, nil
}

func assessPRs(prs []githubstack.PullRequest, baseBranch string, branches []string) []Issue {
	expectedBases := make(map[string]string, len(branches))
	base := baseBranch
	for _, branch := range branches {
		expectedBases[branch] = base
		base = branch
	}
	byHead := make(map[string][]githubstack.PullRequest, len(prs))
	for _, pr := range prs {
		byHead[pr.Head] = append(byHead[pr.Head], pr)
	}
	issues := make([]Issue, 0)
	for _, branch := range branches {
		matches := byHead[branch]
		switch len(matches) {
		case 0:
			issues = append(issues, Issue{Branch: branch, Reason: "no open pull request"})
		case 1:
			pr := matches[0]
			if pr.State != "OPEN" {
				issues = append(issues, Issue{Branch: branch, Reason: strings.ToLower(pr.State) + " pull request"})
				continue
			}
			if expected := expectedBases[branch]; pr.Base != expected {
				issues = append(issues, Issue{Branch: branch, Reason: fmt.Sprintf("PR #%d base %s, want %s", pr.Number, pr.Base, expected)})
			}
		default:
			issues = append(issues, Issue{Branch: branch, Reason: "multiple pull requests"})
		}
	}
	return issues
}

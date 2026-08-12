// Package link plans and applies a Graphite-authoritative GitHub stack link.
package link

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/shhac/gt2gh/internal/githubstack"
	"github.com/shhac/gt2gh/internal/graphite"
)

// Git provides the read-only local repository facts needed for a plan.
type Git interface {
	CurrentBranch(context.Context) (string, error)
	LocalBranches(context.Context) ([]string, error)
	Clean(context.Context) error
}

// Graphite discovers Graphite's declared ancestry without a checkout.
type Graphite interface {
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
	Trunk        string
	Branches     []string
	PullRequests []githubstack.PullRequest
}

// Discover resolves a Graphite path and read-only GitHub PR facts without
// deciding whether their current native-stack relationship is safe to link.
// sync uses this to report divergence before choosing its own apply policy.
func (s Service) Discover(ctx context.Context, requestedBranch string) (Plan, error) {
	if s.Git == nil || s.Graphite == nil || s.GitHub == nil {
		return Plan{}, fmt.Errorf("link service is not fully configured")
	}
	target, source, err := resolveTarget(ctx, s.Git, requestedBranch)
	if err != nil {
		return Plan{}, err
	}
	localBranches, err := s.Git.LocalBranches(ctx)
	if err != nil {
		return Plan{}, err
	}
	local := branchSet(localBranches)
	if !local[target] {
		return Plan{}, fmt.Errorf("selected branch %q is not a local branch", target)
	}

	stack, err := s.Graphite.Discover(ctx, target)
	if err != nil {
		return Plan{}, err
	}
	if err := validateStackLocalAndSafe(local, stack); err != nil {
		return Plan{}, err
	}

	prs, err := s.GitHub.Inspect(ctx, stack.Branches)
	if err != nil {
		return Plan{}, err
	}
	return Plan{Target: target, TargetSource: source, Trunk: stack.Trunk, Branches: stack.Branches, PullRequests: prs}, nil
}

// Plan applies link's stricter policy: existing pull requests must already
// have the expected base relationship. sync deliberately has a separate,
// explicit reconciliation policy for detected divergence.
func (s Service) Plan(ctx context.Context, requestedBranch string) (Plan, error) {
	plan, err := s.Discover(ctx, requestedBranch)
	if err != nil {
		return Plan{}, err
	}
	if err := validatePRs(plan.PullRequests, plan.Trunk, plan.Branches); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

func resolveTarget(ctx context.Context, git Git, requestedBranch string) (string, string, error) {
	if requestedBranch != "" {
		return requestedBranch, "--branch", nil
	}
	target, err := git.CurrentBranch(ctx)
	if err != nil {
		return "", "", err
	}
	return target, "current Git branch", nil
}

func branchSet(branches []string) map[string]bool {
	set := make(map[string]bool, len(branches))
	for _, branch := range branches {
		set[branch] = true
	}
	return set
}

func validateStackLocalAndSafe(local map[string]bool, stack graphite.Stack) error {
	if !local[stack.Trunk] {
		return fmt.Errorf("declared Graphite trunk %q is not a local branch", stack.Trunk)
	}
	if strings.HasPrefix(stack.Trunk, "-") {
		return fmt.Errorf("declared Graphite trunk %q cannot be passed safely to gh stack link", stack.Trunk)
	}
	for _, branch := range stack.Branches {
		if !local[branch] {
			return fmt.Errorf("Graphite stack branch %q is not a local branch", branch)
		}
		if strings.HasPrefix(branch, "-") {
			return fmt.Errorf("Graphite stack branch %q cannot be passed safely to gh stack link", branch)
		}
	}
	return nil
}

// Apply revalidates all discovery and local state immediately before invoking
// gh, and refuses if the revalidated plan differs from the preview.
func (s Service) Apply(ctx context.Context, requestedBranch string, preview Plan) (Plan, error) {
	if s.Git == nil || s.Graphite == nil || s.GitHub == nil {
		return Plan{}, fmt.Errorf("link service is not fully configured")
	}
	if err := s.Git.Clean(ctx); err != nil {
		return Plan{}, err
	}
	plan, err := s.Plan(ctx, requestedBranch)
	if err != nil {
		return Plan{}, err
	}
	if !plan.Equal(preview) {
		return Plan{}, fmt.Errorf("link plan changed during revalidation; rerun without --apply to review the new plan")
	}
	if err := s.GitHub.Link(ctx, plan.Trunk, plan.Branches); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

// Equal compares every fact that can affect the command shown in a preview or
// the GitHub action performed after revalidation.
func (left Plan) Equal(right Plan) bool {
	if left.Target != right.Target || left.TargetSource != right.TargetSource || left.Trunk != right.Trunk || !sameStrings(left.Branches, right.Branches) || len(left.PullRequests) != len(right.PullRequests) {
		return false
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

func validatePRs(prs []githubstack.PullRequest, trunk string, branches []string) error {
	seen := make(map[string]bool, len(prs))
	expectedBases := make(map[string]string, len(branches))
	base := trunk
	for _, branch := range branches {
		expectedBases[branch] = base
		base = branch
	}
	for _, pr := range prs {
		if seen[pr.Head] {
			return fmt.Errorf("GitHub returned multiple pull requests for branch %q; refusing ambiguous link", pr.Head)
		}
		seen[pr.Head] = true
		if pr.State != "OPEN" {
			return fmt.Errorf("branch %q has %s pull request #%d; refusing to relink it", pr.Head, strings.ToLower(pr.State), pr.Number)
		}
		if expected := expectedBases[pr.Head]; pr.Base != expected {
			return fmt.Errorf("branch %q has open pull request #%d based on %q, want %q; refusing divergent GitHub stack", pr.Head, pr.Number, pr.Base, expected)
		}
	}
	return nil
}

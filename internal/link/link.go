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
	GraphitePath []string
	Base         string
	BaseSource   string
	Branches     []string
	PullRequests []githubstack.PullRequest
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
	if err := validatePathLocalAndSafe(local, stack.Path); err != nil {
		return Plan{}, err
	}

	base, baseSource, branches, err := selectBoundary(stack.Path, stack.Trunks, requestedTrunk)
	if err != nil {
		return Plan{}, err
	}
	prs, err := s.GitHub.Inspect(ctx, branches)
	if err != nil {
		return Plan{}, err
	}
	return Plan{
		Target:       target,
		TargetSource: source,
		GraphitePath: append([]string(nil), stack.Path...),
		Base:         base,
		BaseSource:   baseSource,
		Branches:     branches,
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
	plan, err := s.DiscoverWithTrunk(ctx, requestedBranch, requestedTrunk)
	if err != nil {
		return Plan{}, err
	}
	if err := validatePRs(plan.PullRequests, plan.Base, plan.Branches); err != nil {
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

func validatePathLocalAndSafe(local map[string]bool, path []string) error {
	if len(path) < 2 {
		return fmt.Errorf("selected branch has no Graphite ancestor that can be used as a link base")
	}
	for _, branch := range path {
		if !local[branch] {
			return fmt.Errorf("Graphite ancestry branch %q is not a local branch", branch)
		}
		if strings.HasPrefix(branch, "-") {
			return fmt.Errorf("Graphite ancestry branch %q cannot be passed safely to gh stack link", branch)
		}
	}
	return nil
}

// selectBoundary chooses only among Graphite-declared trunk candidates on the
// selected ancestry. It never guesses by branch name. An explicit override is
// accepted only for one of those candidates; otherwise exactly one candidate
// is required.
func selectBoundary(path, trunks []string, requestedTrunk string) (string, string, []string, error) {
	if len(path) < 2 {
		return "", "", nil, fmt.Errorf("selected branch has no Graphite ancestor that can be used as a link base")
	}
	declared := make(map[string]bool, len(trunks))
	for _, trunk := range trunks {
		declared[trunk] = true
	}
	indices := make(map[string]int)
	for index, branch := range path[:len(path)-1] {
		if declared[branch] {
			indices[branch] = index
		}
	}
	if requestedTrunk != "" {
		index, valid := indices[requestedTrunk]
		if !valid {
			if !declared[requestedTrunk] {
				return "", "", nil, fmt.Errorf("requested trunk %q is not a Graphite-declared trunk", requestedTrunk)
			}
			return "", "", nil, fmt.Errorf("requested trunk %q is not an ancestor of selected branch %q", requestedTrunk, path[len(path)-1])
		}
		return requestedTrunk, "--trunk", append([]string(nil), path[index+1:]...), nil
	}
	if len(indices) == 1 {
		for trunk, index := range indices {
			return trunk, "Graphite-declared ancestry", append([]string(nil), path[index+1:]...), nil
		}
	}
	if len(indices) == 0 {
		return "", "", nil, fmt.Errorf("selected Graphite ancestry %q has no declared trunk; use supported Graphite configuration to resolve it", strings.Join(path, " -> "))
	}
	candidates := make([]string, 0, len(indices))
	for trunk := range indices {
		candidates = append(candidates, trunk)
	}
	sort.Strings(candidates)
	return "", "", nil, fmt.Errorf("selected Graphite ancestry has multiple declared trunks (%s); rerun with --trunk <branch>", strings.Join(candidates, ", "))
}

// Apply revalidates all discovery and local state immediately before invoking
// gh, and refuses if the revalidated plan differs from the preview.
func (s Service) Apply(ctx context.Context, requestedBranch string, preview Plan) (Plan, error) {
	return s.ApplyWithTrunk(ctx, requestedBranch, "", preview)
}

func (s Service) ApplyWithTrunk(ctx context.Context, requestedBranch, requestedTrunk string, preview Plan) (Plan, error) {
	if s.Git == nil || s.Graphite == nil || s.GitHub == nil {
		return Plan{}, fmt.Errorf("link service is not fully configured")
	}
	if err := s.Git.Clean(ctx); err != nil {
		return Plan{}, err
	}
	plan, err := s.PlanWithTrunk(ctx, requestedBranch, requestedTrunk)
	if err != nil {
		return Plan{}, err
	}
	if !plan.Equal(preview) {
		return Plan{}, fmt.Errorf("link plan changed during revalidation; rerun without --apply to review the new plan")
	}
	if err := s.GitHub.Link(ctx, plan.Base, plan.Branches); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

// Equal compares every fact that can affect the command shown in a preview or
// the GitHub action performed after revalidation.
func (left Plan) Equal(right Plan) bool {
	if left.Target != right.Target || left.TargetSource != right.TargetSource || left.Base != right.Base || left.BaseSource != right.BaseSource || !sameStrings(left.GraphitePath, right.GraphitePath) || !sameStrings(left.Branches, right.Branches) || len(left.PullRequests) != len(right.PullRequests) {
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

// TrunkCompletions derives deterministic, local Graphite trunk candidates
// from a no-checkout discovery pass.
func (s Service) TrunkCompletions(ctx context.Context, prefix string) ([]string, error) {
	if s.Git == nil || s.Graphite == nil {
		return nil, fmt.Errorf("link service is not fully configured")
	}
	local, err := s.Git.LocalBranches(ctx)
	if err != nil {
		return nil, err
	}
	target, err := s.Git.CurrentBranch(ctx)
	if err != nil {
		return nil, err
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

func validatePRs(prs []githubstack.PullRequest, baseBranch string, branches []string) error {
	seen := make(map[string]bool, len(prs))
	expectedBases := make(map[string]string, len(branches))
	base := baseBranch
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

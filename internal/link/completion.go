package link

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

func branchSet(branches []string) map[string]bool {
	set := make(map[string]bool, len(branches))
	for _, branch := range branches {
		set[branch] = true
	}
	return set
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
	available := branchSet(local)
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

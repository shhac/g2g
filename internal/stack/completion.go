package stack

import (
	"context"
	"sort"
	"strings"

	"github.com/shhac/gt2gh/internal/graphite"
)

// CompletionGraphite adds the read-only lookups shell completion needs on top
// of path discovery.
type CompletionGraphite interface {
	Discover(context.Context, string) (graphite.Stack, error)
	TrackedBranches(context.Context) ([]string, error)
}

// Completions supplies deterministic, checkout-free shell-completion
// candidates. It lives beside discovery rather than inside any one command's
// package, so a command does not have to depend on another just to complete a
// --branch flag.
type Completions struct {
	Git      Git
	Graphite CompletionGraphite
}

func (c Completions) configured() bool { return c.Git != nil && c.Graphite != nil }

// Branches returns only locally-present Graphite branches, sorted for
// deterministic completion. It neither inspects nor changes checkout state.
func (c Completions) Branches(ctx context.Context, prefix string) ([]string, error) {
	if !c.configured() {
		return nil, errNotConfigured
	}
	local, err := c.Git.LocalBranches(ctx)
	if err != nil {
		return nil, err
	}
	tracked, err := c.Graphite.TrackedBranches(ctx)
	if err != nil {
		return nil, err
	}
	return matching(tracked, branchSet(local), prefix), nil
}

// Trunks derives local Graphite trunk candidates from a no-checkout discovery
// pass over the selected target.
func (c Completions) Trunks(ctx context.Context, target, prefix string) ([]string, error) {
	if !c.configured() {
		return nil, errNotConfigured
	}
	local, err := c.Git.LocalBranches(ctx)
	if err != nil {
		return nil, err
	}
	if target == "" {
		if target, err = c.Git.CurrentBranch(ctx); err != nil {
			return nil, err
		}
	}
	declared, err := c.Graphite.Discover(ctx, target)
	if err != nil {
		return nil, err
	}
	return matching(declared.Trunks, branchSet(local), prefix), nil
}

func matching(candidates []string, available map[string]bool, prefix string) []string {
	var matches []string
	for _, candidate := range candidates {
		if available[candidate] && strings.HasPrefix(candidate, prefix) {
			matches = append(matches, candidate)
		}
	}
	sort.Strings(matches)
	return matches
}

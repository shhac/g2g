package restack

import (
	"context"
	"slices"

	"github.com/shhac/g2g/internal/graph"
)

// stranded is every local branch outside the selection whose recorded parent
// this rewrite moves.
//
// Only a direct child is named: what is stacked on it stays on it, so it is
// brought along by whatever brings the child. Each is named with the root of
// the rewrite it hangs from, the lowest moving branch below it, because that
// root's subtree is the one selection that covers it and everything the
// rewrite already took.
func (s Service) stranded(ctx context.Context, discovery graph.Discovery, steps []Step) ([]Stranded, error) {
	moving := make(map[string]bool, len(steps))
	for _, step := range steps {
		moving[step.Branch] = true
	}
	local, err := s.Git.LocalBranches(ctx)
	if err != nil {
		return nil, err
	}
	left := []Stranded{}
	for _, step := range steps {
		for _, child := range discovery.Graph.Children(step.Branch) {
			if slices.Contains(discovery.Branches, child) || !slices.Contains(local, child) {
				continue
			}
			left = append(left, Stranded{Branch: child, Parent: step.Branch, Root: rootOf(discovery.Graph, moving, step.Branch)})
		}
	}
	return left, nil
}

// rootOf is the lowest moving branch a moving branch is stacked on.
func rootOf(g graph.Graph, moving map[string]bool, branch string) string {
	for {
		parent, tracked := g.Parent(branch)
		if !tracked || !moving[parent] {
			return branch
		}
		branch = parent
	}
}

// behind is each trunk a step replays onto whose remote's copy, as this
// checkout last saw it, is ahead of it: only ahead, never diverged, because a
// rewritten upstream is not one this checkout merely lags.
//
// Local refs only. A restack never fetches, and the question is whether what
// is already here says the replay is onto a stale trunk. It only ever adds a
// note, so a question Git cannot answer says nothing rather than failing the
// plan.
func (s Service) behind(ctx context.Context, discovery graph.Discovery, steps []Step) []TrunkBehind {
	upstream, ok := s.Git.(graph.Upstream)
	if !ok {
		return nil
	}
	found := []TrunkBehind{}
	asked := map[string]bool{}
	for _, step := range steps {
		trunk := step.Parent
		if asked[trunk] || discovery.Graph.Tracked(trunk) {
			continue
		}
		asked[trunk] = true
		known, err := upstream.KnownParent(ctx, trunk)
		if err != nil || known == "" || known == step.Base {
			continue
		}
		forward, err := s.Git.IsAncestor(ctx, step.Base, known)
		if err != nil || !forward {
			continue
		}
		_, commits, err := s.Git.Divergence(ctx, step.Base, known)
		if err != nil {
			continue
		}
		found = append(found, TrunkBehind{Trunk: trunk, Commits: commits})
	}
	return found
}

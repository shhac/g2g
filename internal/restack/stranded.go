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
			left = append(left, Stranded{Branch: child, Parent: step.Branch, Root: rootOf(steps, step)})
		}
	}
	return left, nil
}

// rootOf is the lowest moving branch step is stacked on: the root of the
// replay it is part of. Steps come parents first, so the walk is down a
// list rather than up a graph, and cannot loop.
func rootOf(steps []Step, step Step) string {
	root := step.Branch
	for index := slices.IndexFunc(steps, func(each Step) bool { return each.Branch == step.Branch }); index > 0; index-- {
		if steps[index-1].Branch == step.Parent {
			root, step = steps[index-1].Branch, steps[index-1]
		}
	}
	return root
}

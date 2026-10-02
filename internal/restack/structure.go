package restack

import (
	"context"
	"maps"
	"slices"

	"github.com/shhac/g2g/internal/graph"
)

// recordStructure writes back what the rewrite actually produced: any branch
// it reparented, and where every branch now forks.
//
// Reparenting has to be recorded here rather than by the caller. A rewrite
// with --onto moves a fragment onto a different base, and leaving the recorded
// parent naming the old one would make the graph describe a structure that no
// longer exists — which every later command, including the next restack, would
// then measure against.
//
// It walks the whole selection rather than the plan's steps. Once a rewrite
// succeeds the re-derived plan has no steps left, so recording only those
// would record nothing at all and leave every fork point describing the world
// before the rewrite.
//
// A branch that is not actually built on its parent is skipped: writing its
// parent's tip as a fork point would assert a range that does not exist.
func (s Service) recordStructure(ctx context.Context, branches []string, reparent map[string]string) error {
	adopted, err := s.Graph.Store.Load(ctx)
	if err != nil {
		return err
	}
	updated, err := reparentStructure(adopted, reparent)
	if err != nil {
		return err
	}
	if err := s.refreshForkPoints(ctx, updated, branches); err != nil {
		return err
	}
	return s.Graph.Store.Save(ctx, updated)
}

// reparentStructure updates only the declared parent relationships. Keeping it
// separate from fork-point refresh makes the two records a restack repairs
// independently visible at their natural seams.
func reparentStructure(adopted graph.Graph, reparent map[string]string) (graph.Graph, error) {
	updated := adopted.Clone()
	for _, branch := range slices.Sorted(maps.Keys(reparent)) {
		parent := reparent[branch]
		edge, tracked := updated.Edges[branch]
		if !tracked || edge.Parent == parent {
			continue
		}
		edge.Parent = parent
		// Adopt keeps the trunk set true on both sides: a new base that nothing
		// else hangs from becomes a root, and a branch that has just gained one
		// stops being one.
		adoptedGraph, _, err := updated.Adopt(branch, edge)
		if err != nil {
			return graph.Graph{}, err
		}
		updated = adoptedGraph
	}
	return updated, nil
}

// refreshForkPoints records the parent tips that describe each replay range.
func (s Service) refreshForkPoints(ctx context.Context, updated graph.Graph, branches []string) error {
	for _, branch := range branches {
		edge, tracked := updated.Edges[branch]
		if !tracked {
			continue
		}
		built, err := s.Git.IsAncestor(ctx, edge.Parent, branch)
		if err != nil {
			return err
		}
		if !built {
			continue
		}
		forkPoint, err := s.Git.Resolve(ctx, edge.Parent)
		if err != nil {
			return err
		}
		edge.ForkPoint = forkPoint
		updated.Edges[branch] = edge
		if err := s.Git.PinForkPoint(ctx, branch, forkPoint); err != nil {
			return err
		}
	}
	return nil
}

package graph

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
)

// UntrackPlan removes edges from the adopted graph.
type UntrackPlan struct {
	Discovery
	// Removed is the branches whose edges the write drops.
	Removed []string
	// Undeclared is the declared trunks the write stops being trunks. A trunk
	// nobody declared is not here: untracking one would strand every stack on
	// it for a command that has always done nothing to it.
	Undeclared []string
	// Dependents is the declared trunks that land into an undeclared one. They
	// are left as they are, and named.
	Dependents []string
	// Orphaned is the branches left pointing at a removed parent. They are
	// reported rather than reparented.
	Orphaned []string
	Updated  Graph
}

// Equal compares everything that changes what the write does.
func (p UntrackPlan) Equal(other UntrackPlan) bool {
	return p.Discovery.Equal(other.Discovery) &&
		slices.Equal(p.Removed, other.Removed) &&
		slices.Equal(p.Undeclared, other.Undeclared) &&
		slices.Equal(p.Dependents, other.Dependents) &&
		slices.Equal(p.Orphaned, other.Orphaned) &&
		p.Updated.Equal(other.Updated)
}

// NoOp reports a selection with nothing to untrack or undeclare.
func (p UntrackPlan) NoOp() bool { return len(p.Removed) == 0 && len(p.Undeclared) == 0 }

// PlanUntrack removes the selected branches from the graph. Scope decides how
// many: branch alone, or the branch and everything under it.
func (s Service) PlanUntrack(ctx context.Context, selection Selection) (UntrackPlan, error) {
	if selection.Scope != ScopeSubtree {
		selection.Scope = ScopeBranch
	}
	discovery, err := s.Discover(ctx, selection)
	if err != nil {
		return UntrackPlan{}, err
	}
	removed := make([]string, 0, len(discovery.Branches))
	undeclared := make([]string, 0)
	dependents := make([]string, 0)
	for _, branch := range discovery.Branches {
		if discovery.Graph.Tracked(branch) {
			removed = append(removed, branch)
		}
		if discovery.Graph.IsDeclared(branch) {
			undeclared = append(undeclared, branch)
			dependents = append(dependents, discovery.Graph.Dependents(branch)...)
		}
	}
	updated := discovery.Graph.Untrack(removed...)
	for _, branch := range undeclared {
		updated = updated.Undeclare(branch)
	}
	orphaned := make([]string, 0)
	for _, branch := range updated.Orphans() {
		if !slices.Contains(discovery.Graph.Orphans(), branch) {
			orphaned = append(orphaned, branch)
		}
	}
	return UntrackPlan{Discovery: discovery, Removed: removed, Undeclared: undeclared, Dependents: dependents, Orphaned: orphaned, Updated: updated}, nil
}

// ApplyUntrack writes the adopted graph.
func (s Service) ApplyUntrack(ctx context.Context, plan UntrackPlan) error {
	if plan.NoOp() {
		return fmt.Errorf("no tracked branches were selected")
	}
	diagnostic.Event(ctx, "graph.untrack.apply", diagnostic.Field{Key: "branches", Value: strings.Join(plan.Removed, ",")})
	if err := s.Store.Save(ctx, plan.Updated); err != nil {
		return err
	}
	if s.Refs == nil {
		return nil
	}
	unpinned := make([]string, 0, len(plan.Removed))
	for _, branch := range plan.Removed {
		if err := s.Refs.UnpinForkPoint(ctx, branch); err != nil {
			return s.restoreUntracked(ctx, plan.Discovery.Graph, unpinned, err)
		}
		unpinned = append(unpinned, branch)
	}
	return nil
}

func (s Service) restoreUntracked(ctx context.Context, previous Graph, unpinned []string, applyErr error) error {
	if err := s.Store.Save(ctx, previous); err != nil {
		return fmt.Errorf("%w; could not restore the previous graph: %v", applyErr, err)
	}
	for _, branch := range unpinned {
		if pinErr := s.pin(ctx, branch, previous.Edges[branch].ForkPoint); pinErr != nil {
			return fmt.Errorf("%w; could not restore fork-point pin for %q: %v", applyErr, branch, pinErr)
		}
	}
	return applyErr
}

// Recording and removing one branch's parent.
//
// track and untrack share this file because they are two directions of one
// decision: which branch sits under which. Whole-stack adoption is a different
// question — where does this stack begin — and lives in stack.go.
package graph

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
)

// TrackPlan records one branch under one parent.
type TrackPlan struct {
	Discovery
	// Parent is the resolved parent, empty when the user has not chosen one.
	Parent string
	// Candidates is the ordered list a preview offers when Parent is empty.
	Candidates []Candidate
	// NewTrunk names a parent that is about to become a root of the forest.
	NewTrunk string
	Updated  Graph
	// Blocked is why an apply would refuse, empty when it would proceed.
	Blocked string
}

// Equal compares everything that changes what the write does.
func (p TrackPlan) Equal(other TrackPlan) bool {
	return p.Discovery.Equal(other.Discovery) &&
		p.Parent == other.Parent &&
		p.NewTrunk == other.NewTrunk &&
		p.Blocked == other.Blocked &&
		slices.Equal(p.Candidates, other.Candidates) &&
		p.Updated.Equal(other.Updated)
}

// PlanTrack resolves a parent for the selected branch.
//
// With no parent given it previews the ordered candidates and blocks. Choosing
// one for the user is exactly the guess this tool does not make: the nearest
// ancestor is usually right, and "usually" is not a basis for writing down a
// structure every later command trusts.
func (s Service) PlanTrack(ctx context.Context, selection Selection, parent string) (TrackPlan, error) {
	selection.Scope = ScopeBranch
	discovery, err := s.Discover(ctx, selection)
	if err != nil {
		return TrackPlan{}, err
	}
	candidates, err := Candidates(ctx, s.Git, discovery.Target, s.knownRoots(discovery.Graph))
	if err != nil {
		return TrackPlan{}, err
	}
	plan := TrackPlan{Discovery: discovery, Parent: parent, Candidates: candidates, Updated: discovery.Graph}
	if parent == "" {
		plan.Blocked = "no parent chosen"
		return plan, nil
	}
	if err := s.validateParent(ctx, discovery.Target, parent); err != nil {
		plan.Blocked = err.Error()
		return plan, nil
	}
	forkPoint, err := s.Git.Resolve(ctx, parent)
	if err != nil {
		return TrackPlan{}, err
	}
	updated, newTrunk, err := discovery.Graph.Adopt(discovery.Target, Edge{
		Parent: parent,
		Origin: originOf(parent, candidates),
		// Recorded now, because after the parent is merged and deleted there
		// is nothing left to derive it from.
		ForkPoint: forkPoint,
	})
	if err != nil {
		plan.Blocked = err.Error()
		return plan, nil
	}
	// A parent that is not itself tracked becomes a root of the forest. Saying
	// so is what lets the next branch in the stack find it as a candidate once
	// the trunk has moved past being an ancestor.
	plan.NewTrunk = newTrunk
	plan.Updated = updated
	return plan, nil
}

// originOf records whether Git already agrees with the edge. A parent that is
// an ancestor is confirmed; anything else is the user asserting a relationship
// the commits do not yet show, which is worth saying out loud before it is
// written down.
func originOf(parent string, candidates []Candidate) Origin {
	for _, candidate := range candidates {
		if candidate.Branch == parent && candidate.Ancestor {
			return OriginAncestry
		}
	}
	return OriginUser
}

// knownRoots is where trunk candidates come from: whatever the graph already
// records as a trunk, plus the roots its edges imply.
func (s Service) knownRoots(g Graph) []string {
	roots := slices.Clone(g.Trunks)
	for _, root := range g.Roots() {
		if !slices.Contains(roots, root) {
			roots = append(roots, root)
		}
	}
	return roots
}

func (s Service) validateParent(ctx context.Context, target, parent string) error {
	if parent == target {
		return fmt.Errorf("branch %q cannot be its own parent", target)
	}
	local, err := s.Git.LocalBranches(ctx)
	if err != nil {
		return err
	}
	if !slices.Contains(local, parent) {
		return fmt.Errorf("parent %q is not a local branch", parent)
	}
	return nil
}

// UntrackPlan removes edges from the adopted graph.
type UntrackPlan struct {
	Discovery
	// Removed is the branches whose edges the write drops.
	Removed []string
	// Orphaned is the branches left pointing at a removed parent. They are
	// reported rather than reparented.
	Orphaned []string
	Updated  Graph
}

// Equal compares everything that changes what the write does.
func (p UntrackPlan) Equal(other UntrackPlan) bool {
	return p.Discovery.Equal(other.Discovery) &&
		slices.Equal(p.Removed, other.Removed) &&
		slices.Equal(p.Orphaned, other.Orphaned) &&
		p.Updated.Equal(other.Updated)
}

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
	for _, branch := range discovery.Branches {
		if discovery.Graph.Tracked(branch) {
			removed = append(removed, branch)
		}
	}
	updated := discovery.Graph.Untrack(removed...)
	orphaned := make([]string, 0)
	for _, branch := range updated.Orphans() {
		if !slices.Contains(discovery.Graph.Orphans(), branch) {
			orphaned = append(orphaned, branch)
		}
	}
	return UntrackPlan{Discovery: discovery, Removed: removed, Orphaned: orphaned, Updated: updated}, nil
}

// Revalidate re-reads the world and refuses if anything moved since preview.
func (s Service) RevalidateTrack(ctx context.Context, selection Selection, parent string, preview TrackPlan) (TrackPlan, error) {
	plan, err := s.PlanTrack(ctx, selection, parent)
	if err != nil {
		return TrackPlan{}, err
	}
	return plan, matched(ctx, "graph.track", plan.Equal(preview))
}

// RevalidateUntrack re-reads the world and refuses if anything moved.
func (s Service) RevalidateUntrack(ctx context.Context, selection Selection, preview UntrackPlan) (UntrackPlan, error) {
	plan, err := s.PlanUntrack(ctx, selection)
	if err != nil {
		return UntrackPlan{}, err
	}
	return plan, matched(ctx, "graph.untrack", plan.Equal(preview))
}

func matched(ctx context.Context, event string, equal bool) error {
	return diagnostic.Revalidated(ctx, event, "graph", equal)
}

// ApplyTrack writes the adopted graph. It refuses a blocked plan rather than
// writing a structure the preview said it would not.
func (s Service) ApplyTrack(ctx context.Context, plan TrackPlan) error {
	if plan.Blocked != "" {
		return fmt.Errorf("cannot track %q: %s", plan.Target, plan.Blocked)
	}
	diagnostic.Event(ctx, "graph.track.apply", diagnostic.Field{Key: "branch", Value: plan.Target}, diagnostic.Field{Key: "parent", Value: plan.Parent})
	if err := s.Store.Save(ctx, plan.Updated); err != nil {
		return err
	}
	if err := s.pin(ctx, plan.Target, plan.Updated.Edges[plan.Target].ForkPoint); err != nil {
		return s.rollbackGraph(ctx, plan.Discovery.Graph, err)
	}
	return nil
}

// ApplyUntrack writes the adopted graph.
func (s Service) ApplyUntrack(ctx context.Context, plan UntrackPlan) error {
	if len(plan.Removed) == 0 {
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

// rollbackGraph returns an apply failure after restoring the graph that was
// current when its plan was made. A pin is auxiliary durability state; it must
// not leave an adopted edge behind when it cannot be created.
func (s Service) rollbackGraph(ctx context.Context, previous Graph, applyErr error) error {
	if err := s.Store.Save(ctx, previous); err != nil {
		return fmt.Errorf("%w; could not restore the previous graph: %v", applyErr, err)
	}
	return applyErr
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

// pin keeps a fork point reachable. A repository without a pinner still
// records the fork point; it is only unprotected against collection.
func (s Service) pin(ctx context.Context, branch, forkPoint string) error {
	if s.Refs == nil || forkPoint == "" {
		return nil
	}
	return s.Refs.PinForkPoint(ctx, branch, forkPoint)
}

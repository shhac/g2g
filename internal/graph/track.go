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
	// Refreshed means the parent is the one already recorded and only the fork
	// point is written again: see refresh.
	Refreshed bool
	Updated   Graph
	// Blocked is why an apply would refuse, empty when it would proceed.
	Blocked string
}

// Equal compares everything that changes what the write does.
func (p TrackPlan) Equal(other TrackPlan) bool {
	return p.Discovery.Equal(other.Discovery) &&
		p.Parent == other.Parent &&
		p.NewTrunk == other.NewTrunk &&
		p.Refreshed == other.Refreshed &&
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
	plan := TrackPlan{Discovery: discovery, Parent: parent, Updated: discovery.Graph}
	// The candidates are what a preview offers in place of a parent, so they
	// are measured only when there is no usable one: measuring every possible
	// parent to record one the user named was a process per local branch that
	// create paid on every branch it made.
	blocked := "no parent chosen"
	if parent != "" {
		blocked = ""
		if err := s.validateParent(ctx, discovery.Target, parent); err != nil {
			blocked = err.Error()
		}
	}
	if blocked != "" {
		plan.Blocked = blocked
		plan.Candidates, err = Candidates(ctx, s.Git, discovery.Target, s.knownRoots(discovery.Graph))
		return plan, err
	}
	if recorded, tracked := discovery.Graph.Edges[discovery.Target]; tracked && recorded.Parent == parent {
		plan, err := s.refresh(ctx, plan, recorded)
		if err != nil || plan.Blocked != "" {
			return plan, err
		}
		plan.Updated, plan.NewTrunk = plan.Updated.Rooted(parent)
		return plan, nil
	}
	origin, err := s.ancestryOf(ctx, parent, discovery.Target)
	if err != nil {
		return TrackPlan{}, err
	}
	forkPoint, err := s.trackFork(ctx, parent, discovery.Target, origin, !discovery.Graph.Tracked(parent))
	if err != nil {
		return TrackPlan{}, err
	}
	// Naming a declared trunk here is the one way its declaration ends; every
	// other path that records an edge refuses it.
	current := discovery.Graph
	if current.IsDeclared(discovery.Target) {
		current = current.Undeclare(discovery.Target)
	}
	updated, newTrunk, err := current.Adopt(discovery.Target, Edge{
		Parent: parent,
		Origin: origin,
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

// refresh answers a track naming the parent already recorded.
//
// Where the recorded fork point still holds, it is preserved unless upstream
// ancestry proves it includes trunk commits. Writing a feature parent's new
// tip over it would hide a parent that moved, which restack needs to see. Where it does not — the branch was rewritten by hand,
// or the commit is gone — the parent is right and the fork point is wrong, and
// this is how to say so. It was a no-op, so the repair doctor names for both
// states changed nothing and doctor named it again.
//
// A trunk that advanced still has an evidenced merge base. A rewritten feature
// parent is different: its former work cannot be distinguished from the child's
// by a merge base, so refreshing still requires its tip in the child.
func (s Service) refresh(ctx context.Context, plan TrackPlan, recorded Edge) (TrackPlan, error) {
	switch plan.States[plan.Target] {
	case StateMovedOffParent, StateForkUnresolvable:
	default:
		// A valid boundary can still include trunk commits when the local
		// trunk was stale at tracking time. Advance it only on upstream
		// ancestry evidence; feature-parent boundaries remain unchanged.
		if !plan.Graph.IsTrunk(recorded.Parent) {
			return plan, nil
		}
		fork, err := s.trackFork(ctx, recorded.Parent, plan.Target, recorded.Origin, true)
		if err != nil {
			return TrackPlan{}, err
		}
		if fork == recorded.ForkPoint {
			return plan, nil
		}
		forward, err := s.Git.IsAncestor(ctx, recorded.ForkPoint, fork)
		if err != nil || !forward {
			return plan, err
		}
		updated := plan.Graph.Clone()
		recorded.ForkPoint = fork
		updated.Edges[plan.Target] = recorded
		plan.Updated, plan.Refreshed = updated, true
		return plan, nil
	}
	built, err := s.Git.IsAncestor(ctx, recorded.Parent, plan.Target)
	if err != nil {
		return TrackPlan{}, err
	}
	if !built && !plan.Graph.IsTrunk(recorded.Parent) && plan.DefaultTrunk != recorded.Parent {
		plan.Blocked = fmt.Sprintf("%s is not built on %s's tip, so where it leaves %s cannot be read from ancestry · record the parent it is built on now", plan.Target, recorded.Parent, recorded.Parent)
		return plan, nil
	}
	origin := OriginUser
	if built {
		origin = OriginAncestry
	}
	forkPoint, err := s.trackFork(ctx, recorded.Parent, plan.Target, origin, plan.Graph.IsTrunk(recorded.Parent))
	if err != nil {
		return TrackPlan{}, err
	}
	updated := plan.Graph.Clone()
	updated.Edges[plan.Target] = Edge{Parent: recorded.Parent, Origin: origin, ForkPoint: forkPoint}
	plan.Updated, plan.Refreshed = updated, true
	return plan, nil
}

// trackFork keeps the replay boundary inside the branch even when its named
// parent has advanced. Readers without merge-base support must refuse that
// case rather than record a parent's unreachable tip.
func (s Service) trackFork(ctx context.Context, parent, target string, origin Origin, trunk bool) (string, error) {
	var fork string
	var err error
	forks, canMerge := s.Git.(interface {
		MergeBase(context.Context, string, string) (string, error)
	})
	if origin == OriginAncestry {
		fork, err = s.Git.Resolve(ctx, parent)
	} else if canMerge {
		fork, err = forks.MergeBase(ctx, parent, target)
	} else {
		return "", fmt.Errorf("cannot determine where %s leaves %s: Git reader does not support merge bases", target, parent)
	}
	if err != nil || !trunk || !canMerge {
		return fork, err
	}
	known, ok := s.Git.(interface {
		KnownParent(context.Context, string) (string, error)
	})
	if !ok {
		return fork, nil
	}
	// Local knowledge only: tracking must never fetch or need a network.
	tip, err := known.KnownParent(ctx, parent)
	if err != nil || tip == "" {
		return fork, err
	}
	// A rewritten or divergent upstream is not evidence that the local
	// trunk merely lagged. Never replace a boundary on that basis.
	forward, err := s.Git.IsAncestor(ctx, parent, tip)
	if err != nil || !forward {
		return fork, err
	}
	shared, err := forks.MergeBase(ctx, tip, target)
	if err != nil {
		return "", err
	}
	forward, err = s.Git.IsAncestor(ctx, fork, shared)
	if err != nil || !forward {
		return fork, err
	}
	return shared, nil
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

// ancestryOf is originOf for one named parent, asked of Git directly rather
// than read from a list of every candidate measured to find it.
func (s Service) ancestryOf(ctx context.Context, parent, target string) (Origin, error) {
	ancestor, err := s.Git.IsAncestor(ctx, parent, target)
	if err != nil || !ancestor {
		return OriginUser, err
	}
	return OriginAncestry, nil
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

// Revalidate re-reads the world and refuses if anything moved since preview.
func (s Service) RevalidateTrack(ctx context.Context, selection Selection, parent string, preview TrackPlan) (TrackPlan, error) {
	plan, err := s.PlanTrack(ctx, selection, parent)
	if err != nil {
		return TrackPlan{}, err
	}
	return plan, matched(ctx, "graph.track", plan.Equal(preview))
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

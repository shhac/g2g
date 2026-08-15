package graph

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/shhac/gt2gh/internal/diagnostic"
)

// Service reads and adopts g2g-owned graphs. It needs Git and a store, and
// notably not Graphite or GitHub: a g2g-owned graph is exactly the structure
// that exists without either.
type Service struct {
	Git   Ancestry
	Store Store
}

// Selection is the no-checkout selector the graph commands share.
type Selection struct {
	Branch string
	Scope  Scope
}

// Discovery is the read-only picture every graph command starts from.
type Discovery struct {
	Graph        Graph
	Target       string
	TargetSource string
	Scope        Scope
	// Branches is the selected set in render order.
	Branches []string
	States   map[string]NodeState
	// StorePath names the file an apply would write, so a preview can be
	// specific about what it is about to change.
	StorePath string
}

// Equal compares every fact that can change what a graph command does.
func (d Discovery) Equal(other Discovery) bool {
	return d.Target == other.Target &&
		d.TargetSource == other.TargetSource &&
		d.Scope == other.Scope &&
		d.StorePath == other.StorePath &&
		d.Graph.Equal(other.Graph) &&
		slices.Equal(d.Branches, other.Branches) &&
		maps.Equal(d.States, other.States)
}

// Orphans reports selected branches whose recorded parent is neither tracked
// nor a trunk.
func (d Discovery) Orphans() []string {
	all := d.Graph.Orphans()
	orphans := make([]string, 0)
	for _, branch := range d.Branches {
		if slices.Contains(all, branch) {
			orphans = append(orphans, branch)
		}
	}
	return orphans
}

// NeedsRestack reports selected branches whose parent moved underneath them.
// gt2gh does not rebase, so this is reported and never repaired.
func (d Discovery) NeedsRestack() []string {
	return d.branchesInState(StateNeedsRestack)
}

// MissingParents reports selected branches whose recorded parent is no longer
// a local branch, which is what a merged and deleted parent looks like.
func (d Discovery) MissingParents() []string {
	return d.branchesInState(StateParentMissing)
}

func (d Discovery) branchesInState(want NodeState) []string {
	matching := make([]string, 0)
	for _, branch := range d.Branches {
		if d.States[branch] == want {
			matching = append(matching, branch)
		}
	}
	return matching
}

// Discover loads the adopted graph and assesses the selected branches against
// Git. It never writes and never checks a branch out.
func (s Service) Discover(ctx context.Context, selection Selection) (Discovery, error) {
	if s.Git == nil || s.Store == nil {
		return Discovery{}, fmt.Errorf("graph service is not fully configured")
	}
	target, source, err := s.target(ctx, selection.Branch)
	if err != nil {
		return Discovery{}, err
	}
	scope, err := ParseScope(string(selection.Scope))
	if err != nil {
		return Discovery{}, err
	}
	adopted, err := s.Store.Load(ctx)
	if err != nil {
		return Discovery{}, err
	}
	branches, err := adopted.Select(target, scope)
	if err != nil {
		return Discovery{}, err
	}
	states, err := Assess(ctx, s.Git, adopted, branches)
	if err != nil {
		return Discovery{}, err
	}
	path, err := s.Store.Path(ctx)
	if err != nil {
		return Discovery{}, err
	}
	diagnostic.Event(ctx, "graph.discovery",
		diagnostic.Field{Key: "target", Value: target},
		diagnostic.Field{Key: "source", Value: source},
		diagnostic.Field{Key: "scope", Value: string(scope)},
		diagnostic.Field{Key: "tracked", Value: fmt.Sprint(len(adopted.Edges))},
		diagnostic.Field{Key: "selected", Value: strings.Join(branches, ",")},
	)
	return Discovery{Graph: adopted, Target: target, TargetSource: source, Scope: scope, Branches: branches, States: states, StorePath: path}, nil
}

func (s Service) target(ctx context.Context, requested string) (string, string, error) {
	if requested != "" {
		return requested, "--branch", nil
	}
	current, err := s.Git.CurrentBranch(ctx)
	if err != nil {
		return "", "", err
	}
	return current, "current Git branch", nil
}

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
	updated, err := discovery.Graph.Track(discovery.Target, Edge{Parent: parent, Authority: AuthorityG2G, Origin: originOf(parent, candidates)})
	if err != nil {
		plan.Blocked = err.Error()
		return plan, nil
	}
	// A parent that is not itself tracked becomes a root of the forest. Saying
	// so is what lets the next branch in the stack find it as a candidate once
	// the trunk has moved past being an ancestor.
	if !discovery.Graph.Tracked(parent) && !discovery.Graph.IsTrunk(parent) {
		plan.NewTrunk = parent
		updated = updated.WithTrunks(append(slices.Clone(discovery.Graph.Trunks), parent)...)
	}
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
	if !equal {
		diagnostic.Event(ctx, event+".revalidation", diagnostic.Field{Key: "match", Value: "false"})
		return fmt.Errorf("graph changed during revalidation; rerun without --apply to review the new plan")
	}
	diagnostic.Event(ctx, event+".revalidation", diagnostic.Field{Key: "match", Value: "true"})
	return nil
}

// ApplyTrack writes the adopted graph. It refuses a blocked plan rather than
// writing a structure the preview said it would not.
func (s Service) ApplyTrack(ctx context.Context, plan TrackPlan) error {
	if plan.Blocked != "" {
		return fmt.Errorf("cannot track %q: %s", plan.Target, plan.Blocked)
	}
	diagnostic.Event(ctx, "graph.track.apply", diagnostic.Field{Key: "branch", Value: plan.Target}, diagnostic.Field{Key: "parent", Value: plan.Parent})
	return s.Store.Save(ctx, plan.Updated)
}

// ApplyUntrack writes the adopted graph.
func (s Service) ApplyUntrack(ctx context.Context, plan UntrackPlan) error {
	if len(plan.Removed) == 0 {
		return fmt.Errorf("no tracked branches were selected")
	}
	diagnostic.Event(ctx, "graph.untrack.apply", diagnostic.Field{Key: "branches", Value: strings.Join(plan.Removed, ",")})
	return s.Store.Save(ctx, plan.Updated)
}

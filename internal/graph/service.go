package graph

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/shhac/gt2gh/internal/diagnostic"
)

// Service reads and adopts g2g-owned graphs. It needs Git and a store, and
// notably not Graphite or GitHub: a g2g-owned graph is exactly the structure
// that exists without either.
// Pinner keeps recorded fork points reachable. It is separate from Ancestry
// because Ancestry is read-only by contract and this writes a ref.
type Pinner interface {
	PinForkPoint(ctx context.Context, branch, object string) error
	UnpinForkPoint(ctx context.Context, branch string) error
}

type Service struct {
	Git   Ancestry
	Store Store
	// Refs pins fork points so they survive garbage collection. A service
	// without one still works; its fork points are simply unprotected.
	Refs Pinner
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

// InState returns the selected branches in one state, in render order.
func (d Discovery) InState(want NodeState) []string { return d.branchesInState(want) }

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
	return s.pin(ctx, plan.Target, plan.Updated.Edges[plan.Target].ForkPoint)
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
	for _, branch := range plan.Removed {
		if err := s.Refs.UnpinForkPoint(ctx, branch); err != nil {
			return err
		}
	}
	return nil
}

// pin keeps a fork point reachable. A repository without a pinner still
// records the fork point; it is only unprotected against collection.
func (s Service) pin(ctx context.Context, branch, forkPoint string) error {
	if s.Refs == nil || forkPoint == "" {
		return nil
	}
	return s.Refs.PinForkPoint(ctx, branch, forkPoint)
}

// StackPlan records a whole linear ancestry in one step.
//
// It is a separate plan from TrackPlan because it answers a different question:
// TrackPlan asks "which parent for this branch", and blocks so the user can
// choose. This asks "where does this stack begin", takes that one answer, and
// derives the rest.
type StackPlan struct {
	Discovery
	// Trunk is where the adoption stops, either named or the only recorded
	// root on the ancestry.
	Trunk string
	// Record is the edges the write would add, ordered trunk first so a parent
	// is always recorded before the branches naming it.
	Record []Adoption
	// Already is the edges both the graph and the ancestry agree on.
	Already []string
	// Conflicts are branches recorded under a different parent. They block:
	// rewriting them would undo a deliberate choice, which bulk adoption of all
	// things must not do quietly.
	Conflicts []string
	NewTrunk  string
	Updated   Graph
	Blocked   string
}

// Adoption is one edge a stack adoption would record.
type Adoption struct {
	Branch string
	Parent string
}

// Branches names the branches this plan would record.
func (p StackPlan) Branches() []string {
	names := make([]string, 0, len(p.Record))
	for _, adoption := range p.Record {
		names = append(names, adoption.Branch)
	}
	return names
}

// Equal compares everything that changes what the write does.
func (p StackPlan) Equal(other StackPlan) bool {
	return p.Discovery.Equal(other.Discovery) &&
		p.Trunk == other.Trunk &&
		p.NewTrunk == other.NewTrunk &&
		p.Blocked == other.Blocked &&
		slices.Equal(p.Record, other.Record) &&
		slices.Equal(p.Already, other.Already) &&
		slices.Equal(p.Conflicts, other.Conflicts) &&
		p.Updated.Equal(other.Updated)
}

// PlanStack works out how to record the whole ancestry between a trunk and the
// selected branch.
//
// One command instead of one per branch, and one that does not need the user to
// already know the structure the tool has just measured for them.
func (s Service) PlanStack(ctx context.Context, selection Selection, trunk string) (StackPlan, error) {
	selection.Scope = ScopeBranch
	discovery, err := s.Discover(ctx, selection)
	if err != nil {
		return StackPlan{}, err
	}
	candidates, err := Candidates(ctx, s.Git, discovery.Target, s.knownRoots(discovery.Graph))
	if err != nil {
		return StackPlan{}, err
	}

	plan := StackPlan{Discovery: discovery, Trunk: trunk, Updated: discovery.Graph}
	if plan.Trunk == "" {
		if plan.Trunk, err = TrunkFor(candidates, s.knownRoots(discovery.Graph)); err != nil {
			plan.Blocked = err.Error()
			return plan, nil
		}
	}
	chain, err := Chain(candidates, plan.Trunk)
	if err != nil {
		plan.Blocked = err.Error()
		return plan, nil
	}

	spine := append(chain, discovery.Target)
	edges, err := s.branches(ctx, spine, plan.Trunk, discovery.Graph)
	if err != nil {
		plan.Blocked = err.Error()
		return plan, nil
	}
	plan.Record, plan.Already, plan.Conflicts = compare(discovery.Graph, spine, edges)
	if len(plan.Conflicts) != 0 {
		plan.Blocked = fmt.Sprintf("the graph already records a different parent for %s · untrack to re-record, or use g2g track --parent for one branch", strings.Join(plan.Conflicts, ", "))
		return plan, nil
	}
	if len(plan.Record) == 0 {
		return plan, nil
	}
	if plan.Updated, plan.NewTrunk, err = s.record(ctx, discovery.Graph, plan.Record, candidates); err != nil {
		return StackPlan{}, err
	}
	return plan, nil
}

// branches grows the spine into the tree the user is working in: every local
// branch whose nearest ancestor is already selected joins, and then every
// branch hanging off those, until nothing new attaches.
//
// A branch sitting directly on the trunk never joins. It is a separate stack
// that happens to share a base, and sweeping it in because it is technically a
// descendant would adopt half the repository.
func (s Service) branches(ctx context.Context, spine []string, trunk string, adopted Graph) ([]Adoption, error) {
	local, err := s.Git.LocalBranches(ctx)
	if err != nil {
		return nil, err
	}
	selected := make([]string, 0, len(spine))
	for _, branch := range spine {
		if branch != trunk {
			selected = append(selected, branch)
		}
	}

	edges := make([]Adoption, 0)
	for grew := true; grew; {
		grew = false
		for _, branch := range local {
			if branch == trunk || contains(selected, branch) {
				continue
			}
			candidates, err := Candidates(ctx, s.Git, branch, s.knownRoots(adopted))
			if err != nil {
				return nil, err
			}
			parent, attached, err := Attach(candidates, selected)
			if err != nil {
				return nil, err
			}
			if !attached {
				continue
			}
			edges = append(edges, Adoption{Branch: branch, Parent: parent})
			selected = append(selected, branch)
			grew = true
		}
	}
	sort.Slice(edges, func(left, right int) bool { return edges[left].Branch < edges[right].Branch })
	return edges, nil
}

// compare splits the derived structure into what has to be recorded, what
// already agrees, and what disagrees. It is pure so the decision matrix needs
// no Git.
func compare(adopted Graph, spine []string, attached []Adoption) (record []Adoption, already, conflicts []string) {
	derived := make([]Adoption, 0, len(spine)+len(attached))
	for index := 1; index < len(spine); index++ {
		derived = append(derived, Adoption{Branch: spine[index], Parent: spine[index-1]})
	}
	derived = append(derived, attached...)

	record, already, conflicts = []Adoption{}, []string{}, []string{}
	for _, edge := range derived {
		recorded, tracked := adopted.Parent(edge.Branch)
		switch {
		case tracked && recorded == edge.Parent:
			already = append(already, edge.Branch)
		case tracked:
			conflicts = append(conflicts, edge.Branch)
		default:
			record = append(record, edge)
		}
	}
	return record, already, conflicts
}

// record applies the adoptions, resolving a fork point per edge as it goes.
func (s Service) record(ctx context.Context, adopted Graph, adoptions []Adoption, candidates []Candidate) (Graph, string, error) {
	updated, promoted := adopted, ""
	for _, adoption := range adoptions {
		forkPoint, err := s.Git.Resolve(ctx, adoption.Parent)
		if err != nil {
			return Graph{}, "", err
		}
		next, trunk, err := updated.Adopt(adoption.Branch, Edge{
			Parent:    adoption.Parent,
			Origin:    originOf(adoption.Parent, candidates),
			ForkPoint: forkPoint,
		})
		if err != nil {
			return Graph{}, "", err
		}
		if trunk != "" {
			promoted = trunk
		}
		updated = next
	}
	return updated, promoted, nil
}

// RevalidateStack recomputes immediately before the write.
func (s Service) RevalidateStack(ctx context.Context, selection Selection, trunk string, preview StackPlan) (StackPlan, error) {
	current, err := s.PlanStack(ctx, selection, trunk)
	if err != nil {
		return StackPlan{}, err
	}
	return current, matched(ctx, "track.stack", current.Equal(preview))
}

// ApplyStack writes the recorded chain and pins each fork point.
func (s Service) ApplyStack(ctx context.Context, plan StackPlan) error {
	if plan.Blocked != "" {
		return fmt.Errorf("cannot record this stack: %s", plan.Blocked)
	}
	if len(plan.Record) == 0 {
		return nil
	}
	if err := s.Store.Save(ctx, plan.Updated); err != nil {
		return err
	}
	for _, adoption := range plan.Record {
		if err := s.pin(ctx, adoption.Branch, plan.Updated.Edges[adoption.Branch].ForkPoint); err != nil {
			return err
		}
	}
	return nil
}

// Recording a whole stack in one step.
//
// track asks which parent for one branch and blocks so the user can answer.
// This asks where the stack begins, takes that one answer, and derives the rest
// from ancestry — a different question, so a different plan.
package graph

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/shhac/g2g/internal/repair"
)

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
	// Repair is Blocked in the shape a caller can lay out. Blocked is rendered
	// from it, so the sentence and the column cannot name different commands.
	Repair repair.Note
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
		plan.Repair = repair.Note{
			Reason: fmt.Sprintf("the graph already records a different parent for %s", strings.Join(plan.Conflicts, ", ")),
			Ways: []repair.Step{
				{Command: "g2g untrack", Effect: "forget the recorded parent so it can be recorded again"},
				{Command: "g2g track --parent", Effect: "record one branch's parent deliberately"},
			},
		}
		plan.Blocked = plan.Repair.Sentence()
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

	// Membership, ordering and the per-branch candidate list are three separate
	// jobs, and the loop previously did all three the same way: by rescanning.
	// selected stays the ordered slice Attach reads; chosen answers "already
	// taken" without walking it.
	chosen := map[string]bool{trunk: true}
	for _, branch := range selected {
		chosen[branch] = true
	}
	// adopted does not change while this runs, so its roots do not either, and
	// a branch's candidates depend only on the branch and those roots. Asking
	// Git again on every pass was work proportional to passes rather than to
	// branches.
	roots := s.knownRoots(adopted)
	candidatesFor := make(map[string][]Candidate, len(local))
	lookup := func(branch string) ([]Candidate, error) {
		if cached, found := candidatesFor[branch]; found {
			return cached, nil
		}
		candidates, err := Candidates(ctx, s.Git, branch, roots)
		if err != nil {
			return nil, err
		}
		candidatesFor[branch] = candidates
		return candidates, nil
	}

	edges := make([]Adoption, 0)
	for grew := true; grew; {
		grew = false
		for _, branch := range local {
			if chosen[branch] {
				continue
			}
			candidates, err := lookup(branch)
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
			chosen[branch] = true
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
	pinned := make([]string, 0, len(plan.Record))
	for _, adoption := range plan.Record {
		if err := s.pin(ctx, adoption.Branch, plan.Updated.Edges[adoption.Branch].ForkPoint); err != nil {
			return s.restoreStack(ctx, plan.Discovery.Graph, pinned, err)
		}
		pinned = append(pinned, adoption.Branch)
	}
	return nil
}

func (s Service) restoreStack(ctx context.Context, previous Graph, pinned []string, applyErr error) error {
	for _, branch := range pinned {
		if err := s.Refs.UnpinForkPoint(ctx, branch); err != nil {
			return fmt.Errorf("%w; could not remove fork-point pin for %q: %v", applyErr, branch, err)
		}
	}
	return s.rollbackGraph(ctx, previous, applyErr)
}

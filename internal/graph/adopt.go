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
	// Repair is why an apply would refuse and the ways out, empty when it
	// would proceed.
	Repair repair.Note
}

// Blocked is why an apply would refuse, as one sentence, empty when it would
// proceed.
func (p StackPlan) Blocked() string { return p.Repair.Sentence() }

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
		p.Repair.Equal(other.Repair) &&
		slices.Equal(p.Record, other.Record) &&
		slices.Equal(p.Already, other.Already) &&
		slices.Equal(p.Conflicts, other.Conflicts) &&
		p.Updated.Equal(other.Updated)
}

// NoOp reports whether applying the plan would change nothing. Recording no
// edge is not enough to say so: naming a stranded branch as the trunk records
// only that it is one.
func (p StackPlan) NoOp() bool { return p.Updated.Equal(p.Graph) }

// AdoptScopes are how much of a stack adopt records. stack is the chain and
// everything that grows from it; path is the chain alone, for somebody taking
// up one line of a stack another person published, whose other branches are
// not theirs to record.
var AdoptScopes = []Scope{ScopeStack, ScopePath}

// PlanStack works out how to record the whole ancestry between a trunk and the
// selected branch, and with ScopePath nothing beyond that chain.
//
// One command instead of one per branch, and one that does not need the user to
// already know the structure the tool has just measured for them.
func (s Service) PlanStack(ctx context.Context, selection Selection, trunk string) (StackPlan, error) {
	chainOnly := selection.Scope == ScopePath
	selection.Scope = ScopeBranch
	discovery, err := s.Discover(ctx, selection)
	if err != nil {
		return StackPlan{}, err
	}
	// related for the same reason the fan-out below uses it: everything that
	// reads these candidates -- trunkFor, chain, originOf -- filters on
	// Ancestor, so the fallback's whole result would be discarded. Where it
	// would have fired, this plan is about to refuse anyway for want of a
	// trunk that is an ancestor, and it now does so without measuring every
	// branch in the repository first.
	candidates, err := related(ctx, s.Git, discovery.Target, s.knownRoots(discovery.Graph))
	if err != nil {
		return StackPlan{}, err
	}

	plan := StackPlan{Discovery: discovery, Trunk: trunk, Updated: discovery.Graph}
	if plan.Trunk == "" {
		if plan.Trunk, err = trunkFor(candidates, s.knownRoots(discovery.Graph)); err != nil {
			plan.Repair = repair.Note{Reason: err.Error()}
			return plan, nil
		}
	}
	below, err := chain(candidates, discovery.Target, plan.Trunk)
	if err != nil {
		plan.Repair = repair.Note{Reason: err.Error()}
		return plan, nil
	}

	spine := append(below, discovery.Target)
	var edges []Adoption
	if !chainOnly {
		if edges, err = s.branches(ctx, spine, plan.Trunk, discovery.Graph); err != nil {
			plan.Repair = repair.Note{Reason: err.Error()}
			return plan, nil
		}
	}
	plan.Record, plan.Already, plan.Conflicts = compare(discovery.Graph, spine, edges)
	if len(plan.Conflicts) != 0 {
		plan.Repair = conflictRepair(discovery.Graph, plan.Conflicts)
		return plan, nil
	}
	if len(plan.Record) == 0 {
		plan.Updated, plan.NewTrunk = discovery.Graph.Rooted(plan.Trunk)
		return plan, nil
	}
	if plan.Updated, plan.NewTrunk, err = s.record(ctx, discovery.Graph, plan.Record, candidates); err != nil {
		return StackPlan{}, err
	}
	return plan, nil
}

// conflictRepair says what the graph holds instead of the ancestry, and the
// ways out. A declared trunk is named as one, because "a different parent" is
// not what it has — and the likeliest meaning is the stack above it, which
// adopting down to it records without touching it.
func conflictRepair(adopted Graph, conflicts []string) repair.Note {
	declared := make([]string, 0)
	for _, branch := range conflicts {
		if adopted.IsDeclared(branch) {
			declared = append(declared, branch)
		}
	}
	if len(declared) != 0 {
		return DeclaredConflict(declared)
	}
	return repair.Note{
		Reason: fmt.Sprintf("the graph already records a different parent for %s", strings.Join(conflicts, ", ")),
		Ways: []repair.Step{
			{Command: "g2g untrack", Effect: "forget the recorded parent so it can be recorded again"},
			{Command: "g2g track --parent", Effect: "record one branch's parent deliberately"},
		},
	}
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
		switch adopted.Judge(edge.Branch, edge.Parent) {
		case VerdictAgreed:
			already = append(already, edge.Branch)
		case VerdictDiffers, VerdictDeclared:
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

// ApplyStack writes the recorded chain and pins each fork point.
func (s Service) ApplyStack(ctx context.Context, plan StackPlan) error {
	if plan.Blocked() != "" {
		return fmt.Errorf("cannot record this stack: %s", plan.Blocked())
	}
	if plan.NoOp() {
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

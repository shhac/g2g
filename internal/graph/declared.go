// Trunks somebody named.
//
// The trunk set fills itself: a parent nothing records becomes a trunk the
// moment something is recorded on it. A declaration is a different kind of
// fact — somebody said so — and it can carry one more: where the trunk goes
// when it is finished. That is not a stack edge. An edge carries a fork point,
// and a fork point is a range to replay; a trunk other people land into must
// never be replayed, so it has no edge and every walk treats it as a root.
package graph

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/repair"
)

// Declaration is a trunk somebody named. Into is where it lands, empty for a
// trunk that lands nowhere, and By is how, as a merge method's name. By is a
// plain string because this package depends on Git alone; the edge that talks
// to GitHub is the one that checks it.
type Declaration struct {
	Into string
	By   string
}

// Lands reports a declaration that says where the trunk goes.
func (d Declaration) Lands() bool { return d.Into != "" }

// Landing is where branch goes, when it is a trunk declared to land somewhere.
func (g Graph) Landing(branch string) (Declaration, bool) {
	declaration := g.Declared[branch]
	return declaration, declaration.Lands()
}

// IsDeclared reports whether somebody named branch a trunk.
func (g Graph) IsDeclared(branch string) bool {
	_, declared := g.Declared[branch]
	return declared
}

// Declare records branch as a trunk, removing any edge it had.
//
// The old parent does not become Into: where a shared branch lands is the
// user's to say, and a caller that wants it names it.
func (g Graph) Declare(branch string, declaration Declaration) (Graph, error) {
	if branch == "" {
		return Graph{}, fmt.Errorf("a branch is required")
	}
	if declaration.Lands() == (declaration.By == "") {
		return Graph{}, fmt.Errorf("a trunk that lands somewhere names both where and how")
	}
	if declaration.Into == branch {
		return Graph{}, fmt.Errorf("%s cannot land into itself", branch)
	}
	if parent, stacked := g.Parent(declaration.Into); stacked {
		return Graph{}, fmt.Errorf("%s is stacked on %s, and a branch something lands into must not be: a stack is replayed, and what others land into must not move under them", declaration.Into, parent)
	}
	updated := g.Clone()
	delete(updated.Edges, branch)
	if updated.Declared == nil {
		updated.Declared = map[string]Declaration{}
	}
	updated.Declared[branch] = declaration
	updated = updated.withTrunks(append(slices.Clone(updated.Trunks), branch)...)
	if err := updated.Validate(); err != nil {
		return Graph{}, err
	}
	return updated, nil
}

// Undeclare forgets that branch was named a trunk. It stops being one at all:
// what sits on it is stranded rather than kept on a trunk nobody asked for, the
// same as untracking any branch something sits on.
func (g Graph) Undeclare(branch string) Graph {
	updated := g.Clone().withoutTrunk(branch)
	delete(updated.Declared, branch)
	return updated
}

// Verdict is what recording one proposed edge would mean, for a command that
// records many at once from ancestry or another record.
type Verdict int

const (
	// VerdictRecord is an edge the graph has no answer for yet.
	VerdictRecord Verdict = iota
	// VerdictAgreed is an edge the graph already records.
	VerdictAgreed
	// VerdictDiffers is a branch the graph records under another parent.
	VerdictDiffers
	// VerdictDeclared is a branch somebody named a trunk. Recording a parent
	// for it would end the declaration, which only track --parent may do.
	VerdictDeclared
)

// Judge says what recording branch under parent would mean. Every command
// that adopts in bulk asks this one question, so a rule about what they may
// not overwrite is learnt once rather than by each of them.
func (g Graph) Judge(branch, parent string) Verdict {
	recorded, tracked := g.Parent(branch)
	switch {
	case g.IsDeclared(branch):
		return VerdictDeclared
	case tracked && recorded == parent:
		return VerdictAgreed
	case tracked:
		return VerdictDiffers
	}
	return VerdictRecord
}

// DeclaredConflict is the refusal a bulk adoption gives when it meets declared
// trunks, and the ways out, the same from every command that can meet one.
func DeclaredConflict(branches []string) repair.Note {
	first := branches[0]
	return repair.Note{
		Reason: fmt.Sprintf("the graph names %s a trunk, which only g2g track --parent undoes", strings.Join(branches, ", ")),
		Ways: []repair.Step{
			{Command: "g2g adopt --trunk " + first, Effect: "record the stack above it instead"},
			{Command: "g2g track --branch " + first + " --parent", Effect: "put it back in the stack below it"},
		},
	}
}

// Dependents names the declared trunks that land into branch, sorted.
func (g Graph) Dependents(branch string) []string {
	dependents := make([]string, 0)
	for trunk, declaration := range g.Declared {
		if declaration.Into == branch {
			dependents = append(dependents, trunk)
		}
	}
	sort.Strings(dependents)
	return dependents
}

// validateDeclared holds what makes a declaration mean anything: it is a
// trunk, it is not stacked, and what it lands into is not stacked either.
//
// The last is what keeps following Into from looping. Every cycle through a
// mix of edges and landings would need a branch something lands into that
// also has an edge, so with none, a walk along Into alone sees every cycle.
func (g Graph) validateDeclared() error {
	for _, branch := range slices.Sorted(maps.Keys(g.Declared)) {
		declaration := g.Declared[branch]
		switch {
		case !g.IsTrunk(branch):
			return fmt.Errorf("declared trunk %q is not in the trunk set", branch)
		case g.Tracked(branch):
			return fmt.Errorf("declared trunk %q also has a parent", branch)
		case declaration.Into == branch:
			return fmt.Errorf("declared trunk %q lands into itself", branch)
		case declaration.Lands() && g.Tracked(declaration.Into):
			return fmt.Errorf("%q lands into %q, which is stacked", branch, declaration.Into)
		}
		seen := map[string]bool{branch: true}
		for next := declaration.Into; next != ""; next = g.Declared[next].Into {
			if seen[next] {
				return fmt.Errorf("declared trunk %q lands, in the end, into itself", branch)
			}
			seen[next] = true
		}
	}
	return nil
}

// DeclarePlan names one branch a trunk.
type DeclarePlan struct {
	Discovery
	Declaration Declaration
	// Removed is the parent the branch was recorded on, which the declaration
	// takes away; empty when it had none.
	Removed string
	Updated Graph
	// Blocked is why an apply would refuse, empty when it would proceed.
	Blocked string
}

// NoOp reports a declaration the graph already records exactly.
func (p DeclarePlan) NoOp() bool { return p.Blocked == "" && p.Updated.Equal(p.Graph) }

// Replaces is the landing a plan would drop: the trunk already lands somewhere,
// and the declaration being written says somewhere else, or nowhere.
func (p DeclarePlan) Replaces() (Declaration, bool) {
	previous, lands := p.Graph.Landing(p.Target)
	if !lands || previous == p.Declaration {
		return Declaration{}, false
	}
	return previous, true
}

// Equal compares everything that changes what the write does.
func (p DeclarePlan) Equal(other DeclarePlan) bool {
	return p.Discovery.Equal(other.Discovery) &&
		p.Declaration == other.Declaration &&
		p.Removed == other.Removed &&
		p.Blocked == other.Blocked &&
		p.Updated.Equal(other.Updated)
}

// PlanDeclare names the selected branch a trunk, one that lands into
// declaration.Into by declaration.By when those are given.
func (s Service) PlanDeclare(ctx context.Context, selection Selection, declaration Declaration) (DeclarePlan, error) {
	selection.Scope = ScopeBranch
	discovery, err := s.Discover(ctx, selection)
	if err != nil {
		return DeclarePlan{}, err
	}
	plan := DeclarePlan{Discovery: discovery, Declaration: declaration, Updated: discovery.Graph}
	plan.Removed, _ = discovery.Graph.Parent(discovery.Target)
	if declaration.Lands() {
		local, err := s.Git.LocalBranches(ctx)
		if err != nil {
			return DeclarePlan{}, err
		}
		if !slices.Contains(local, declaration.Into) {
			plan.Blocked = fmt.Sprintf("%q, where %s would land, is not a local branch", declaration.Into, discovery.Target)
			return plan, nil
		}
	}
	if plan.Updated, err = discovery.Graph.Declare(discovery.Target, declaration); err != nil {
		plan.Updated, plan.Blocked = discovery.Graph, err.Error()
	}
	return plan, nil
}

// RevalidateDeclare re-reads the world and refuses if anything moved.
func (s Service) RevalidateDeclare(ctx context.Context, selection Selection, declaration Declaration, preview DeclarePlan) (DeclarePlan, error) {
	plan, err := s.PlanDeclare(ctx, selection, declaration)
	if err != nil {
		return DeclarePlan{}, err
	}
	return plan, matched(ctx, "graph.declare", plan.Equal(preview))
}

// ApplyDeclare writes the declaration, and drops the fork-point pin of the edge
// it replaced: a trunk has no range to replay, so nothing needs the commit kept.
func (s Service) ApplyDeclare(ctx context.Context, plan DeclarePlan) error {
	if plan.Blocked != "" {
		return fmt.Errorf("cannot declare %q a trunk: %s", plan.Target, plan.Blocked)
	}
	diagnostic.Event(ctx, "graph.declare.apply", diagnostic.Field{Key: "branch", Value: plan.Target}, diagnostic.Field{Key: "into", Value: plan.Declaration.Into})
	if err := s.Store.Save(ctx, plan.Updated); err != nil {
		return err
	}
	if plan.Removed == "" || s.Refs == nil {
		return nil
	}
	if err := s.Refs.UnpinForkPoint(ctx, plan.Target); err != nil {
		return s.rollbackGraph(ctx, plan.Discovery.Graph, err)
	}
	return nil
}

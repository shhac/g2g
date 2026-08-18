// Package restack rewrites a stack's contents so they match its recorded
// structure.
//
// It is g2g's only resumable operation. Everything else is one-shot:
// preview, apply, done. A rewrite can stop half-way on a conflict that only a
// person can resolve, so it leaves a record behind and every other command has
// to notice.
//
// The package is split by what the code is deciding. plan.go works out what
// would be replayed and onto what, and touches nothing; this file performs it
// and carries the resume verbs; journal.go is what survives between them.
package restack

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/graph"
)

// Revalidate re-reads the world and refuses if anything moved since preview.
func (s Service) Revalidate(ctx context.Context, selection graph.Selection, onto string, absorb bool, preview Plan) (Plan, error) {
	if err := s.Git.Clean(ctx); err != nil {
		return Plan{}, err
	}
	plan, err := s.Plan(ctx, selection, onto, absorb)
	if err != nil {
		return Plan{}, err
	}
	return plan, diagnostic.Revalidated(ctx, "restack", "restack plan", plan.Equal(preview))
}

// Apply performs the rewrite. A plan the preview said applies cleanly is
// replayed without touching the checkout; anything else takes the resumable
// engine, which needs the user's working tree and says so first.
func (s Service) Apply(ctx context.Context, plan Plan) error {
	if plan.Blocked != "" {
		return fmt.Errorf("cannot restack: %s", plan.Blocked)
	}
	if len(plan.Steps) == 0 {
		return nil
	}
	if plan.Absorb {
		return s.absorb(ctx, plan)
	}
	needed, err := s.settleCollapses(ctx, plan)
	if err != nil {
		return err
	}
	if !needed {
		return s.recordStructure(ctx, plan.Discovery.Branches, plan.reparenting())
	}
	if plan.Clean {
		return s.replay(ctx, plan)
	}
	return s.rebase(ctx, plan)
}

// settleCollapses moves the branches with nothing left to contribute and
// reports whether any branch still needs an engine.
//
// Branches that collapse move first so their children are planned against where
// they land, and neither engine ever sees a commit that is already upstream.
// Apply and finish both have to do this, and finish once did only half of it:
// it drove the engine without collapsing first, so a branch that became
// collapsible while a resume was in flight was never moved, the next pass
// computed an identical plan, and the loop hit its own non-convergence guard
// and failed for a case the design has an answer to.
func (s Service) settleCollapses(ctx context.Context, plan Plan) (bool, error) {
	if err := s.collapse(ctx, plan); err != nil {
		return false, err
	}
	return len(plan.rewriting()) != 0, nil
}

// verify checks that the engine did what the plan said, rather than reporting
// success because the command exited zero.
//
// Both engines are external and their behaviour varies by version, so the one
// thing worth asserting is the outcome: every branch that was to be rewritten
// now sits on the base it was aimed at. A rewrite that quietly left a branch
// where it was would otherwise be indistinguishable from one that worked, and
// the stack would look repaired while still being wrong.
func (s Service) verify(ctx context.Context, plan Plan) error {
	for _, step := range plan.rewriting() {
		// Against the parent branch rather than the base recorded in the plan:
		// in a stack the parent is being rewritten too, so its planned tip is
		// exactly the commit it no longer points at.
		built, err := s.Git.IsAncestor(ctx, step.Parent, step.Branch)
		if err != nil {
			return err
		}
		if !built {
			return fmt.Errorf("%s was not replayed onto %s; this Git did not perform the rewrite that was planned", step.Branch, step.Parent)
		}
	}
	return nil
}

// collapse moves the branches whose work is entirely in their new base.
func (s Service) collapse(ctx context.Context, plan Plan) error {
	for _, step := range plan.collapsing() {
		if step.Tip == step.Base {
			continue
		}
		diagnostic.Event(ctx, "restack.collapse", diagnostic.Field{Key: "branch", Value: step.Branch})
		if err := s.Git.UpdateBranch(ctx, step.Branch, step.Base); err != nil {
			return err
		}
	}
	return nil
}

// absorb keeps the commits a parent dropped by re-recording where the branch
// forks, which needs no rewriting at all: the parent's tip is already an
// ancestor of the branch.
func (s Service) absorb(ctx context.Context, plan Plan) error {
	diagnostic.Event(ctx, "restack.absorb", diagnostic.Field{Key: "branches", Value: strings.Join(plan.Branches(), ",")})
	return s.recordStructure(ctx, plan.Discovery.Branches, plan.reparenting())
}

func (s Service) replay(ctx context.Context, plan Plan) error {
	ranges := plan.ranges()
	diagnostic.Event(ctx, "restack.replay", diagnostic.Field{Key: "branches", Value: strings.Join(plan.Branches(), ",")})
	if err := s.Git.Replay(ctx, plan.onto(), ranges); err != nil {
		return err
	}
	if err := s.verify(ctx, plan); err != nil {
		return err
	}
	// A replay moves refs without touching the index, so a user standing on a
	// rewritten branch would otherwise see changes they never made.
	if err := s.Git.ResetKeep(ctx); err != nil {
		return err
	}
	return s.recordStructure(ctx, plan.Discovery.Branches, plan.reparenting())
}

// rebase runs the resumable engine and journals enough to undo the whole
// operation, which git cannot do because it only restores the invocation it is
// running.
func (s Service) rebase(ctx context.Context, plan Plan) error {
	record := Record{
		Onto:     plan.Onto,
		Absorb:   plan.Absorb,
		Branch:   plan.Target,
		Scope:    string(plan.Scope),
		ReturnTo: plan.Target,
		Original: map[string]string{},
		Reparent: plan.reparenting(),
	}
	for _, step := range plan.Steps {
		record.Original[step.Branch] = step.Tip
	}
	if err := s.Journal.Save(ctx, record); err != nil {
		return err
	}
	diagnostic.Event(ctx, "restack.rebase", diagnostic.Field{Key: "branches", Value: strings.Join(plan.Branches(), ",")})
	if err := s.rebaseEach(ctx, plan); err != nil {
		return err
	}
	return s.finish(ctx, record)
}

// rebaseEach replays one branch at a time, bottom-up.
//
// The engines model the work differently and are given it differently. Replay
// takes the whole set at once and needs one shared origin. Rebase moves a
// single line of descent, so each branch is rebased onto the parent it now
// has, re-resolved after that parent has itself moved. Handing rebase the
// whole chain and asking --update-refs to carry the intermediate branches
// works on some versions and not others, and buys nothing that sequencing
// does not.
//
// Stopping part-way is the expected outcome, not a failure: the journal is
// already written and --continue re-derives what is left.
func (s Service) rebaseEach(ctx context.Context, plan Plan) error {
	for _, step := range plan.rewriting() {
		base, err := s.Git.Resolve(ctx, step.Parent)
		if err != nil {
			return err
		}
		if err := s.Git.Rebase(ctx, base, localgit.Range{From: step.ForkPoint, To: step.Branch}); err != nil {
			return err
		}
	}
	return nil
}

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

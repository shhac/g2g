// Package restack rewrites a stack's contents so they match its recorded
// structure.
//
// It is g2g's only resumable operation. Everything else is one-shot:
// preview, apply, done. A rewrite can stop half-way on a conflict that only a
// person can resolve, so it leaves a record behind and every other command has
// to notice.
//
// The package is split by what the code is deciding. plan.go works out what
// would be replayed and onto what; this file coordinates the mutation.
// replay.go and rebase.go drive the engines, checkout.go reconciles the working
// tree, and structure.go records the result. resume.go carries recovery verbs;
// journal.go is what survives between them.
package restack

import (
	"context"
	"fmt"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/graph"
)

// Revalidate re-reads the world and refuses if anything moved since preview.
func (s Service) Revalidate(ctx context.Context, selection graph.Selection, onto Onto, absorb bool, pending Pending, preview Plan) (Plan, error) {
	if err := s.Git.Clean(ctx); err != nil {
		return Plan{}, err
	}
	plan, err := s.Plan(ctx, selection, onto, absorb, pending)
	if err != nil {
		return Plan{}, err
	}
	return plan, diagnostic.Revalidated(ctx, "restack", "restack plan", plan.Equal(preview))
}

// Apply performs the rewrite. A plan the preview said applies cleanly is
// replayed without touching the checkout; anything else takes the resumable
// engine, which needs the user's working tree and says so first.
func (s Service) Apply(ctx context.Context, plan Plan) error {
	if active, err := s.InProgress(ctx); err != nil {
		return err
	} else if active {
		return fmt.Errorf("a restack is already in progress · run g2g restack --continue or g2g restack --abort in the worktree that started it")
	}
	if plan.Blocked != "" {
		return fmt.Errorf("cannot restack: %s", plan.Blocked)
	}
	if len(plan.Steps) == 0 {
		// A rewrite with nothing to replay can still have an edge to record: a
		// branch already sitting on the --onto target has no commits to move
		// and a recorded parent that still names where it used to be.
		if moves := plan.reparenting(); len(moves) != 0 {
			return s.recordStructure(ctx, plan.Discovery.Branches, moves)
		}
		return nil
	}
	if plan.Absorb {
		return s.absorb(ctx, plan)
	}
	// Where the checkout stands before anything moves. A rewrite that moves the
	// branch you are on leaves the index and working tree describing the old
	// commit, so the two ends have to be known to reconcile them.
	standing, err := s.standingOn(ctx)
	if err != nil {
		return err
	}
	if plan.inPlace() {
		return s.applyInPlace(ctx, plan, standing)
	}
	return s.rebase(ctx, plan, standing)
}

// begin persists both rollback state and the checkout's owner before any ref
// moves. Worktree identity is optional for injected Git implementations.
func (s Service) begin(ctx context.Context, plan Plan, standing checkout) (Record, error) {
	record := s.record(plan, standing)
	if locator, ok := s.Git.(WorktreeLocator); ok {
		var err error
		record.Worktree, err = locator.WorktreeDir(ctx)
		if err != nil {
			return Record{}, err
		}
	}
	if starter, ok := s.Journal.(interface {
		Start(context.Context, Record) error
	}); ok {
		return record, starter.Start(ctx, record)
	}
	return record, s.Journal.Save(ctx, record)
}

// record is what survives an interrupted rewrite. ReturnTo is where the
// checkout stood, not the selection's target: the two differ whenever someone
// restacks a stack from outside it, and it was the target that was written.
func (s Service) record(plan Plan, standing checkout) Record {
	record := Record{
		OntoParent:     plan.Onto.Parent,
		Absorb:         plan.Absorb,
		Branch:         plan.Target,
		Scope:          string(plan.Scope),
		ReturnTo:       standing.Branch,
		CheckoutBranch: standing.Branch,
		CheckoutTip:    standing.Tip,
		Original:       map[string]string{},
		Reparent:       plan.reparenting(),
		Structure:      map[string]RecordedEdge{},
	}
	for _, step := range plan.Steps {
		record.Original[step.Branch] = step.Tip
	}
	// Every selected branch rather than every step: recording walks the whole
	// selection, so that is what an abort has to be able to put back.
	for _, branch := range plan.Discovery.Branches {
		if edge, tracked := plan.Discovery.Graph.Edges[branch]; tracked {
			record.Structure[branch] = RecordedEdge{Parent: edge.Parent, ForkPoint: edge.ForkPoint}
		}
	}
	return record
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
//
// Branches that collapse move first so their children are planned against where
// they land, and neither engine ever sees a commit that is already upstream.
// Apply and finish both have to do this, and finish once did only half of it:
// it drove the engine without collapsing first, so a branch that became
// collapsible while a resume was in flight was never moved, the next pass
// computed an identical plan, and the loop hit its own non-convergence guard
// and failed for a case the design has an answer to.
func (s Service) collapse(ctx context.Context, plan Plan) error {
	for _, step := range plan.collapsing() {
		if step.Head == step.Base {
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

// inPlace reports a plan that needs no working tree: everything it replays
// the preview said applies cleanly, or it only collapses.
func (p Plan) inPlace() bool {
	return p.Clean || len(p.rewriting()) == 0
}

// NeedsWorkingTree reports a rewrite Apply will do in the user's own working
// tree: one whose preview found a conflict, or that could not be previewed. A
// caller that must not touch that tree -- one holding somebody's uncommitted
// work -- asks this before applying rather than finding out from git.
func (p Plan) NeedsWorkingTree() bool {
	return len(p.Steps) != 0 && !p.Absorb && !p.inPlace()
}

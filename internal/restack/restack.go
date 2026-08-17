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
			return err
		}
		updated = adoptedGraph
	}
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
	return s.Graph.Store.Save(ctx, updated)
}

// Continue resumes an interrupted restack.
//
// It recomputes rather than replaying a stored queue, so a user who ran git
// rebase --continue or --abort themselves simply changes what work remains.
func (s Service) Continue(ctx context.Context) error {
	record, found, err := s.Journal.Load(ctx)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("no restack is in progress")
	}
	inProgress, err := s.Git.RebaseInProgress(ctx)
	if err != nil {
		return err
	}
	if inProgress {
		if err := s.Git.RebaseContinue(ctx); err != nil {
			return err
		}
	}
	return s.finish(ctx, record)
}

// Skip abandons the commit an interrupted rebase stopped on.
func (s Service) Skip(ctx context.Context) error {
	record, found, err := s.Journal.Load(ctx)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("no restack is in progress")
	}
	if err := s.Git.RebaseSkip(ctx); err != nil {
		return err
	}
	return s.finish(ctx, record)
}

// finish re-derives what is left. Anything still needing a rewrite is done
// now; when nothing is, the graph is brought up to date and the journal goes.
func (s Service) finish(ctx context.Context, record Record) error {
	// Each pass records what has already been rewritten, then asks what is
	// left. The order matters: a branch this operation has replayed no longer
	// sits on the fork point the graph holds for it, and a plan measured
	// against that stale value reads it as moved off its parent. That refuses
	// the whole selection, so a resumed restack used to stop at the branch
	// whose conflict was just resolved and report success while every branch
	// above it stayed on abandoned history.
	//
	for pass := 0; ; pass++ {
		discovery, err := s.Graph.Discover(ctx, record.Selection())
		if err != nil {
			return err
		}
		if err := s.recordStructure(ctx, discovery.Branches, record.Reparent); err != nil {
			return err
		}
		plan, err := s.Plan(ctx, record.Selection(), record.Onto, record.Absorb)
		if err != nil {
			return err
		}
		if len(plan.Steps) == 0 || plan.Blocked != "" {
			// Nothing left to rewrite. The reparenting comes from the record
			// rather than the fresh plan: once the rewrite has happened the
			// branch no longer sits where the graph says, so the plan can no
			// longer tell where it was headed.
			if err := s.recordStructure(ctx, plan.Discovery.Branches, record.Reparent); err != nil {
				return err
			}
			return s.Journal.Clear(ctx)
		}
		// Every pass that finds work rewrites at least one branch, so needing
		// more passes than there are branches means it is not converging.
		if pass > len(plan.Discovery.Branches) {
			return fmt.Errorf("restack did not settle after %d passes · run g2g restack to see the current state", pass)
		}
		needed, err := s.settleCollapses(ctx, plan)
		if err != nil {
			return err
		}
		// Collapsing was the whole of this pass's work: the next one re-plans
		// against where those branches landed and finds nothing left.
		if !needed {
			continue
		}
		if plan.Clean {
			if err := s.replay(ctx, plan); err != nil {
				return err
			}
			return s.Journal.Clear(ctx)
		}
		// Carrying on can stop again on the next branch, which is the same
		// state this began in: the journal stays and --continue re-derives.
		if err := s.rebaseEach(ctx, plan); err != nil {
			return err
		}
	}
}

// Abort restores every branch to the tip it had when the operation began,
// including paths that already completed.
func (s Service) Abort(ctx context.Context) error {
	record, found, err := s.Journal.Load(ctx)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("no restack is in progress")
	}
	inProgress, err := s.Git.RebaseInProgress(ctx)
	if err != nil {
		return err
	}
	if inProgress {
		if err := s.Git.RebaseAbort(ctx); err != nil {
			return err
		}
	}
	diagnostic.Event(ctx, "restack.abort", diagnostic.Field{Key: "branches", Value: strings.Join(slices.Sorted(maps.Keys(record.Original)), ",")})
	for _, branch := range slices.Sorted(maps.Keys(record.Original)) {
		if err := s.Git.UpdateBranch(ctx, branch, record.Original[branch]); err != nil {
			return err
		}
	}
	return s.Journal.Clear(ctx)
}

// Conflicted lists the files an interrupted rewrite left for the user.
func (s Service) Conflicted(ctx context.Context) ([]string, error) {
	return s.Git.ConflictedPaths(ctx)
}

// InProgress reports an unfinished restack, which every other command has to
// refuse while it lasts: a branch may already have moved while the graph still
// records where it used to be.
func (s Service) InProgress(ctx context.Context) (bool, error) {
	if s.Journal == nil {
		return false, nil
	}
	_, found, err := s.Journal.Load(ctx)
	return found, err
}

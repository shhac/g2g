// Package restack rewrites a stack's contents so they match its recorded
// structure.
//
// It is gt2gh's only resumable operation. Everything else is one-shot:
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

	"github.com/shhac/gt2gh/internal/diagnostic"
	"github.com/shhac/gt2gh/internal/graph"
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
	if !plan.Equal(preview) {
		diagnostic.Event(ctx, "restack.revalidation", diagnostic.Field{Key: "match", Value: "false"})
		return Plan{}, fmt.Errorf("restack plan changed during revalidation; rerun without --apply to review the new plan")
	}
	diagnostic.Event(ctx, "restack.revalidation", diagnostic.Field{Key: "match", Value: "true"})
	return plan, nil
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
	if plan.Clean {
		return s.replay(ctx, plan)
	}
	return s.rebase(ctx, plan)
}

// absorb keeps the commits a parent dropped by re-recording where the branch
// forks, which needs no rewriting at all: the parent's tip is already an
// ancestor of the branch.
func (s Service) absorb(ctx context.Context, plan Plan) error {
	diagnostic.Event(ctx, "restack.absorb", diagnostic.Field{Key: "branches", Value: strings.Join(plan.Branches(), ",")})
	return s.recordStructure(ctx, plan)
}

func (s Service) replay(ctx context.Context, plan Plan) error {
	ranges := plan.ranges()
	diagnostic.Event(ctx, "restack.replay", diagnostic.Field{Key: "branches", Value: strings.Join(plan.Branches(), ",")})
	if err := s.Git.Replay(ctx, plan.Steps[0].Base, ranges); err != nil {
		return err
	}
	// A replay moves refs without touching the index, so a user standing on a
	// rewritten branch would otherwise see changes they never made.
	if err := s.Git.ResetKeep(ctx); err != nil {
		return err
	}
	return s.recordStructure(ctx, plan)
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
	}
	for _, step := range plan.Steps {
		record.Original[step.Branch] = step.Tip
	}
	if err := s.Journal.Save(ctx, record); err != nil {
		return err
	}
	diagnostic.Event(ctx, "restack.rebase", diagnostic.Field{Key: "branches", Value: strings.Join(plan.Branches(), ",")})
	if err := s.Git.Rebase(ctx, plan.Steps[0].Base, plan.rebaseRange()); err != nil {
		return err
	}
	return s.finish(ctx, graph.Selection{Branch: plan.Target, Scope: plan.Scope})
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
func (s Service) recordStructure(ctx context.Context, plan Plan) error {
	adopted, err := s.Graph.Store.Load(ctx)
	if err != nil {
		return err
	}
	updated := adopted.Clone()
	for _, step := range plan.Steps {
		edge, tracked := updated.Edges[step.Branch]
		if !tracked || edge.Parent == step.Parent {
			continue
		}
		edge.Parent = step.Parent
		updated.Edges[step.Branch] = edge
		// A new base that nothing else hangs from is a root of the forest, and
		// recording it is what lets the next branch find it as a candidate.
		if !updated.Tracked(step.Parent) && !updated.IsTrunk(step.Parent) {
			updated = updated.WithTrunks(append(slices.Clone(updated.Trunks), step.Parent)...)
		}
	}
	for _, branch := range plan.Discovery.Branches {
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
	return s.finish(ctx, record.Selection())
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
	return s.finish(ctx, record.Selection())
}

// finish re-derives what is left. Anything still needing a rewrite is done
// now; when nothing is, the graph is brought up to date and the journal goes.
func (s Service) finish(ctx context.Context, selection graph.Selection) error {
	plan, err := s.Plan(ctx, selection, "", false)
	if err != nil {
		return err
	}
	if len(plan.Steps) != 0 && plan.Blocked == "" {
		if plan.Clean {
			if err := s.replay(ctx, plan); err != nil {
				return err
			}
			return s.Journal.Clear(ctx)
		}
		return s.Git.Rebase(ctx, plan.Steps[0].Base, plan.rebaseRange())
	}
	if err := s.recordStructure(ctx, plan); err != nil {
		return err
	}
	return s.Journal.Clear(ctx)
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

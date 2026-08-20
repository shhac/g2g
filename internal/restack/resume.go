package restack

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
)

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

type finishOutcome uint8

const (
	finishComplete finishOutcome = iota
	finishAgain
)

// finish re-derives what is left. Each pass first records completed work, then
// either completes, retries after collapses, or advances the selected engine.
func (s Service) finish(ctx context.Context, record Record) error {
	for pass := 0; ; pass++ {
		outcome, err := s.finishPass(ctx, record, pass)
		if err != nil {
			return err
		}
		if outcome == finishComplete {
			return s.Journal.Clear(ctx)
		}
	}
}

// finishPass makes one explicit convergence decision. Recording comes before
// planning because a successful rewrite has moved refs beyond the graph's old
// fork points; planning against those stale points would see false drift.
func (s Service) finishPass(ctx context.Context, record Record, pass int) (finishOutcome, error) {
	discovery, err := s.Graph.Discover(ctx, record.Selection())
	if err != nil {
		return finishComplete, err
	}
	if err := s.recordStructure(ctx, discovery.Branches, record.Reparent); err != nil {
		return finishComplete, err
	}
	plan, err := s.Plan(ctx, record.Selection(), ToBranch(record.OntoParent), record.Absorb)
	if err != nil {
		return finishComplete, err
	}
	if len(plan.Steps) == 0 || plan.Blocked != "" {
		// Reparenting is held by the durable record: after a rewrite, the fresh
		// plan cannot recover where an edge used to point.
		if err := s.recordStructure(ctx, plan.Discovery.Branches, record.Reparent); err != nil {
			return finishComplete, err
		}
		return finishComplete, nil
	}
	// Every working pass changes at least one branch. More passes than branches
	// therefore means the state is not converging.
	if pass > len(plan.Discovery.Branches) {
		return finishComplete, fmt.Errorf("restack did not settle after %d passes · run g2g restack to see the current state", pass)
	}
	// Where the checkout stands before this pass moves anything. A resume
	// finishes through the same engines, so it can strand the working tree the
	// same way.
	standing, err := s.standingOn(ctx)
	if err != nil {
		return finishComplete, err
	}
	needed, err := s.settleCollapses(ctx, plan)
	if err != nil {
		return finishComplete, err
	}
	if !needed {
		if err := s.resettle(ctx, standing); err != nil {
			return finishComplete, err
		}
		return finishAgain, nil
	}
	if plan.Clean {
		if err := s.replay(ctx, plan, standing); err != nil {
			return finishComplete, err
		}
		return finishComplete, nil
	}
	// A rebase may stop again; leaving the journal lets --continue recompute.
	if err := s.rebaseEach(ctx, plan); err != nil {
		return finishComplete, err
	}
	return finishAgain, nil
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

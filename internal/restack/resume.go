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
			return s.conclude(ctx, record)
		}
	}
}

// conclude forgets the operation and puts the user back on the branch they
// started from.
//
// The rebase engine checks out every branch it rewrites, so a restack that
// went through it finished on whichever branch was rebased last -- or, after
// a conflict, the one that stopped -- rather than where the person had been
// standing. The journal is cleared first: the rewrite is done either way, and
// a failure to switch back must not leave every other command refusing.
func (s Service) conclude(ctx context.Context, record Record) error {
	if err := s.Journal.Clear(ctx); err != nil {
		return err
	}
	if record.ReturnTo == "" {
		return nil
	}
	current, err := s.Git.CurrentBranch(ctx)
	if err != nil || current == record.ReturnTo {
		return err
	}
	diagnostic.Event(ctx, "restack.return", diagnostic.Field{Key: "branch", Value: record.ReturnTo})
	if err := s.Git.SwitchBranch(ctx, record.ReturnTo); err != nil {
		return fmt.Errorf("the restack is finished, but switching back to %s failed: %w", record.ReturnTo, err)
	}
	return nil
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
	// No pending: a resume runs after whatever the caller was going to
	// move has already moved.
	plan, err := s.Plan(ctx, record.Selection(), ToBranch(record.OntoParent), record.Absorb, nil)
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
	if plan.inPlace() {
		if err := s.rewriteInPlace(ctx, plan, standing); err != nil {
			return finishComplete, err
		}
		if len(plan.rewriting()) == 0 {
			// Only collapses: what they moved may leave more to do above them.
			return finishAgain, nil
		}
		return finishComplete, nil
	}
	// A rebase may stop again; leaving the journal lets --continue recompute.
	if err := s.collapseAndRebase(ctx, plan, standing); err != nil {
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
	// Where the checkout stands once git has put back what it knows about.
	// Restoring a tip is a bare ref move, and a rebase the user finished by
	// hand has left them on a rewritten branch, so the working tree has to be
	// brought back with it or git reports the whole rewrite as changes nobody
	// made -- under a message saying every branch is back where it started.
	standing, err := s.standingOn(ctx)
	if err != nil {
		return err
	}
	diagnostic.Event(ctx, "restack.abort", diagnostic.Field{Key: "branches", Value: strings.Join(slices.Sorted(maps.Keys(record.Original)), ",")})
	for _, branch := range slices.Sorted(maps.Keys(record.Original)) {
		if err := s.Git.UpdateBranch(ctx, branch, record.Original[branch]); err != nil {
			return err
		}
	}
	if err := s.restoreStructure(ctx, record.Structure); err != nil {
		return err
	}
	if err := s.resettle(ctx, standing); err != nil {
		return err
	}
	return s.conclude(ctx, record)
}

// restoreStructure puts back the edges a resumed pass recorded.
//
// Restoring the tips alone left each branch's fork point at the parent tip the
// pass had recorded, which the restored branch no longer contains, so the next
// plan read every one of them as moved off its parent and refused. A journal
// written before this was recorded carries no structure and restores only the
// tips, as it always did.
func (s Service) restoreStructure(ctx context.Context, structure map[string]RecordedEdge) error {
	if len(structure) == 0 {
		return nil
	}
	adopted, err := s.Graph.Store.Load(ctx)
	if err != nil {
		return err
	}
	parents := make(map[string]string, len(structure))
	for branch, edge := range structure {
		parents[branch] = edge.Parent
	}
	updated, err := reparentStructure(adopted, parents)
	if err != nil {
		return err
	}
	for _, branch := range slices.Sorted(maps.Keys(structure)) {
		edge, tracked := updated.Edges[branch]
		if !tracked {
			continue
		}
		edge.ForkPoint = structure[branch].ForkPoint
		updated.Edges[branch] = edge
		if edge.ForkPoint == "" {
			continue
		}
		if err := s.Git.PinForkPoint(ctx, branch, edge.ForkPoint); err != nil {
			return err
		}
	}
	return s.Graph.Store.Save(ctx, updated)
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

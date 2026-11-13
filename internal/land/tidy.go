package land

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/landed"
	"github.com/shhac/g2g/internal/restack"
	syncer "github.com/shhac/g2g/internal/sync"
)

// tidy brings the rest of the stack onto the advanced trunk and forgets the
// branch that has just landed.
//
// Nothing here can stop the descent. The work is merged; a branch that could
// not be deleted is untidy, and reporting it as a failed land would be wrong
// about the thing that matters and would strand the branches above.
//
// head is the head known to have landed. It is decided before the advance
// rather than read after it, because the replay can move this very branch.
func (s Service) tidy(ctx context.Context, plan Plan, step Step, head string, changed *[]string) error {
	if plan.Queue != nil {
		if err := s.guardSurvivors(ctx, plan, githubstack.Mergeability{}, false); err != nil {
			return err
		}
	}
	advanced, err := s.advance(ctx, plan)
	if advanced {
		*changed = append(*changed, "brought the stack up to date after "+step.Branch)
	}
	if err != nil {
		return err
	}
	// Before forget: a move that is refused keeps the branch recorded, so
	// rerunning land finds it landed and finishes its cleanup. Forgotten
	// first, a rerun had nothing left to tidy and the branch stayed for good.
	if err := s.rehome(ctx, plan, step, changed); err != nil {
		return err
	}
	if err := s.forget(ctx, plan, step.Branch); err != nil {
		return err
	}
	s.remove(ctx, plan, step, head)
	return nil
}

// advance fast-forwards the base and replays what is left onto it.
func (s Service) advance(ctx context.Context, plan Plan) (bool, error) {
	if plan.KeepTrunk {
		return false, s.Git.FetchIsolated(ctx, plan.Options.Remote, []string{plan.Trunk})
	}
	// The top of what remains, always. A guard here read as a special case for
	// the last step and was not one: where it held, index was already the last
	// index, so both branches named the same branch.
	target := plan.Steps[len(plan.Steps)-1].Branch
	synced, err := s.Syncer.Plan(ctx, syncSelection(plan, target), plan.Options.Remote, syncer.TakeNothing, plan.Options.Worktrees)
	if err != nil {
		return false, err
	}
	if synced.Blocked() != "" {
		return false, fmt.Errorf("cannot bring the rest of the stack up to date: %s", synced.Blocked())
	}
	// A worktree that picked up changes since the descent began: its branch
	// would be left on a trunk that no longer exists upstream.
	if unplanned := plan.unplannedLeaves(synced); len(unplanned) != 0 {
		return false, fmt.Errorf("cannot bring the rest of the stack up to date: %s", leftBehind(unplanned).Sentence())
	}
	if synced.Nothing() {
		return false, nil
	}
	if err := s.replayableHere(ctx, plan, synced.Restack); err != nil {
		return false, err
	}
	if err := s.Syncer.Apply(ctx, synced); err != nil {
		// A sync that moved branches before failing has changed the stack as
		// surely as one that finished.
		var partial *syncer.Stopped
		return errors.As(err, &partial), err
	}
	return true, nil
}

// replayableHere stops a descent whose replay would have to be resolved in a
// working tree holding somebody's uncommitted work.
//
// Most replays apply cleanly and never touch the checkout, so a dirty tree is
// no reason to refuse a descent up front. One that conflicts falls back to a
// rebase in this tree, and that is where it would mix a conflict into their
// changes. Nothing has been replayed yet when this refuses: what has merged
// stays merged, and pull does the replay once the tree is clean.
func (s Service) replayableHere(ctx context.Context, plan Plan, replay restack.Plan) error {
	if !replay.NeedsWorkingTree() {
		return nil
	}
	if err := s.Git.Clean(ctx); err != nil {
		return fmt.Errorf("replaying %s onto %s conflicts, and resolving that needs this working tree, which has uncommitted changes · commit them, run g2g pull --apply, then rerun g2g land", strings.Join(replay.Branches(), ", "), plan.Trunk)
	}
	return nil
}

// forget reparents what sat on the landed branch, then drops it from the graph.
//
// In that order. Forgetting first strands the children, which is what prune
// refuses to do; reparenting first leaves the landed branch with none, so the
// refusal never applies. The children keep their own fork points, which is
// what keeps their replay ranges to their own commits.
func (s Service) forget(ctx context.Context, plan Plan, landed string) error {
	if plan.KeepTrunk {
		return s.forgetFetched(ctx, plan, landed)
	}
	adopted, err := s.Graph.Store.Load(ctx)
	if err != nil {
		return err
	}
	edge, tracked := adopted.Edges[landed]
	if !tracked && plan.declared() {
		// No edge to drop and nothing sat on it: the plan refused otherwise.
		return s.undeclare(ctx, landed, plan.Trunk)
	}
	if !tracked {
		return nil
	}
	updated, _, err := adopted.Lift(landed)
	if err != nil {
		return err
	}
	if err := s.Graph.Store.Save(ctx, updated); err != nil {
		return err
	}
	pruned, err := s.Pruner.Plan(ctx, graph.Selection{Branch: landed, Scope: graph.ScopeBranch})
	if err != nil {
		return err
	}
	// A prune that will not forget it stops here, before its refs are
	// deleted. Carrying on left the graph recording a branch that no longer
	// existed, found only later by doctor as "branch missing".
	if pruned.Blocked() != "" {
		return fmt.Errorf("%s merged, and cannot be forgotten: %s", landed, pruned.Blocked())
	}
	if pruned.Nothing() {
		return fmt.Errorf("%s merged, but git does not find its work in %s by content, so it is left recorded · run g2g status to see why", landed, edge.Parent)
	}
	return s.Pruner.Apply(ctx, pruned)
}

// remove deletes the branch's refs, here and on the remote.
//
// Every failure is swallowed and reported as a diagnostic rather than
// returned. The pull request is merged: a ref that would not delete is
// untidiness, and turning it into a failed land would both misreport what
// happened and stop the branches above from landing at all.
//
// The remote branch goes only while it still holds head, the one that landed.
// A reviewer can push to it after the merge, and that commit is on the remote
// alone; a branch somebody has moved on, or recreated, is left where it is.
func (s Service) remove(ctx context.Context, plan Plan, step Step, head string) {
	if plan.Options.DeleteRemote {
		if err := s.Git.DeleteRemoteBranch(ctx, plan.Options.Remote, step.Branch, head); err != nil {
			diagnostic.Warn(ctx, "land.delete_remote", fmt.Sprintf("could not delete %s on %s: %v", step.Branch, plan.Options.Remote, err))
		}
	}
	if !plan.Options.DeleteLocal {
		return
	}
	// git will not delete the branch that is checked out, and landing a whole
	// stack from its top ends standing on the last one deleted.
	if current, err := s.Git.CurrentBranch(ctx); err == nil && current == step.Branch {
		switchAway := func() error { return s.Git.SwitchBranch(ctx, plan.Trunk) }
		switch {
		case plan.KeepTrunk:
			switchAway = func() error { return s.Git.SwitchDetached(ctx, localgit.IsolatedRef(plan.Options.Remote, plan.Trunk)) }
		case plan.Detach:
			switchAway = func() error { return s.Git.SwitchDetached(ctx, plan.Trunk) }
		}
		if err := switchAway(); err != nil {
			diagnostic.Warn(ctx, "land.switch", fmt.Sprintf("could not move off %s to %s: %v", step.Branch, plan.Trunk, err))
			return
		}
	}
	if err := s.Git.DeleteBranch(ctx, step.Branch); err != nil {
		diagnostic.Warn(ctx, "land.delete_local", fmt.Sprintf("could not delete %s: %v", step.Branch, err))
	}
}

// forgetFetched uses the shared landed check against the fetched trunk before
// untracking a leaf. The local trunk is held elsewhere and deliberately stale;
// the ordinary prune would therefore find nothing in it. The structural check
// is repeated in case somebody recorded a child during the descent.
func (s Service) forgetFetched(ctx context.Context, plan Plan, branch string) error {
	recorded, err := s.Graph.Store.Load(ctx)
	if err != nil {
		return err
	}
	limit := recorded.Edges[branch].ForkPoint
	upstream, err := landed.Into(ctx, s.Git, localgit.IsolatedRef(plan.Options.Remote, plan.Trunk), branch, limit)
	if err != nil {
		return err
	}
	if !upstream {
		return fmt.Errorf("%s merged, but its work is not in the fetched %s · rerun g2g land", branch, plan.Trunk)
	}
	// What sat on it was left where its worktree has it, unreplayed, and is
	// recorded on the trunk with the fork point it already had, which keeps
	// its replay to its own commits when that worktree is free. Only a
	// branch the plan left is lifted: anything else recorded on it since the
	// descent began is refused below, as it was.
	if children := recorded.Children(branch); len(children) != 0 && allLeft(plan, children) {
		if recorded, _, err = recorded.Lift(branch); err != nil {
			return err
		}
		if err := s.Graph.Store.Save(ctx, recorded); err != nil {
			return err
		}
	}
	if note := declaredRefusal(recorded, branch); note.Reason != "" {
		return fmt.Errorf("%s", note.Sentence())
	}
	untrack, err := s.Graph.PlanUntrack(ctx, graph.Selection{Branch: branch, Scope: graph.ScopeBranch})
	if err != nil {
		return err
	}
	return s.Graph.ApplyUntrack(ctx, untrack)
}

// allLeft reports branches every one of which the plan leaves where its
// worktree has it.
func allLeft(plan Plan, branches []string) bool {
	for _, branch := range branches {
		if !plan.Worktrees.Leaves(branch) {
			return false
		}
	}
	return true
}

package land

import (
	"context"
	"errors"
	"fmt"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/graph"
	syncer "github.com/shhac/g2g/internal/sync"
)

// tidy brings the rest of the stack onto the advanced trunk and forgets the
// branch that has just landed.
//
// Nothing here can stop the descent. The work is merged; a branch that could
// not be deleted is untidy, and reporting it as a failed land would be wrong
// about the thing that matters and would strand the branches above.
func (s Service) tidy(ctx context.Context, plan Plan, step Step, changed *[]string) error {
	advanced, err := s.advance(ctx, plan)
	if advanced {
		*changed = append(*changed, "brought the stack up to date after "+step.Branch)
	}
	if err != nil {
		return err
	}
	if err := s.forget(ctx, plan, step.Branch); err != nil {
		return err
	}
	s.remove(ctx, plan, step)
	return nil
}

// advance fast-forwards the base and replays what is left onto it.
func (s Service) advance(ctx context.Context, plan Plan) (bool, error) {
	// The top of what remains, always. A guard here read as a special case for
	// the last step and was not one: where it held, index was already the last
	// index, so both branches named the same branch.
	target := plan.Steps[len(plan.Steps)-1].Branch
	synced, err := s.Syncer.Plan(ctx, syncSelection(plan, target), plan.Options.Remote, syncer.TakeNothing)
	if err != nil {
		return false, err
	}
	if synced.Blocked != "" {
		return false, fmt.Errorf("cannot bring the rest of the stack up to date: %s", synced.Blocked)
	}
	if synced.Nothing() {
		return false, nil
	}
	if err := s.Syncer.Apply(ctx, synced); err != nil {
		// A sync that moved branches before failing has changed the stack as
		// surely as one that finished.
		var partial *syncer.Stopped
		return errors.As(err, &partial), err
	}
	return true, nil
}

// forget reparents what sat on the landed branch, then drops it from the graph.
//
// In that order. Forgetting first strands the children, which is what prune
// refuses to do; reparenting first leaves the landed branch with none, so the
// refusal never applies. The children keep their own fork points, which is
// what keeps their replay ranges to their own commits.
func (s Service) forget(ctx context.Context, plan Plan, landed string) error {
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
	if pruned.Blocked != "" {
		return fmt.Errorf("%s merged, and cannot be forgotten: %s", landed, pruned.Blocked)
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
func (s Service) remove(ctx context.Context, plan Plan, step Step) {
	if plan.Options.DeleteRemote {
		if err := s.Git.DeleteRemoteBranch(ctx, plan.Options.Remote, step.Branch); err != nil {
			diagnostic.Warn(ctx, "land.delete_remote", fmt.Sprintf("could not delete %s on %s: %v", step.Branch, plan.Options.Remote, err))
		}
	}
	if !plan.Options.DeleteLocal {
		return
	}
	// git will not delete the branch that is checked out, and landing a whole
	// stack from its top ends standing on the last one deleted.
	if current, err := s.Git.CurrentBranch(ctx); err == nil && current == step.Branch {
		if err := s.Git.SwitchBranch(ctx, plan.Trunk); err != nil {
			diagnostic.Warn(ctx, "land.switch", fmt.Sprintf("could not move off %s to %s: %v", step.Branch, plan.Trunk, err))
			return
		}
	}
	if err := s.Git.DeleteBranch(ctx, step.Branch); err != nil {
		diagnostic.Warn(ctx, "land.delete_local", fmt.Sprintf("could not delete %s: %v", step.Branch, err))
	}
}

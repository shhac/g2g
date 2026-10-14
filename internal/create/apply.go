package create

import (
	"context"
	"errors"
	"fmt"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/graph"
)

// Partial is an apply that created and recorded the branch and then could not
// commit. Both of those stay: the branch is where the user asked for it and
// recorded where they said, and the staged changes are still staged on it.
type Partial struct {
	Branch string
	Parent string
	Err    error
}

func (p *Partial) Error() string {
	return fmt.Sprintf("created and recorded %s, but the commit failed: %v", p.Branch, p.Err)
}
func (p *Partial) Unwrap() error { return p.Err }

// Apply creates the branch, records it, and commits, in that order.
//
// Recording comes before the commit so a recording that fails can be undone
// completely: the branch has nothing of its own yet, so switching back and
// deleting it loses nothing. Committing first would put the staged work in a
// commit that the rollback then deletes.
func (s Service) Apply(ctx context.Context, plan Plan) error {
	if plan.Blocked() != "" {
		return fmt.Errorf("cannot create %q: %s", plan.Name, plan.Blocked())
	}
	diagnostic.Event(ctx, "create.apply", diagnostic.Field{Key: "branch", Value: plan.Name}, diagnostic.Field{Key: "parent", Value: plan.Parent})
	if err := s.Git.CreateBranch(ctx, plan.Name, plan.Parent); err != nil {
		return err
	}
	if err := s.record(ctx, plan); err != nil {
		return s.rollback(ctx, plan, err)
	}
	if !plan.Commit {
		return nil
	}
	if err := s.Git.Commit(ctx, plan.Message); err != nil {
		return &Partial{Branch: plan.Name, Parent: plan.Parent, Err: err}
	}
	return nil
}

// record writes the edge exactly as track would, and refuses one that is not
// the edge the preview promised.
func (s Service) record(ctx context.Context, plan Plan) error {
	tracked, err := s.Graph.PlanTrack(ctx, graph.Selection{Branch: plan.Name}, plan.Parent)
	if err != nil {
		return err
	}
	if tracked.Blocked() != "" {
		return errors.New(tracked.Blocked())
	}
	// The parent can only have moved in the moment between the revalidation
	// and the switch, and a fork point other than the commit the branch
	// started at would make its first restack replay the wrong range.
	if forkPoint := tracked.Updated.Edges[plan.Name].ForkPoint; forkPoint != plan.At {
		return fmt.Errorf("%s moved while %s was being created", plan.Parent, plan.Name)
	}
	return s.Graph.ApplyTrack(ctx, tracked)
}

// rollback puts the checkout back where it was and removes the branch, which
// has no commits of its own yet. It says what it could not undo rather than
// hiding it behind the error that caused it.
func (s Service) rollback(ctx context.Context, plan Plan, cause error) error {
	if err := s.switchBack(ctx, plan); err != nil {
		return &RolledBack{Branch: plan.Name, Cause: cause, Left: err}
	}
	if err := s.Git.DeleteBranch(ctx, plan.Name); err != nil {
		return &RolledBack{Branch: plan.Name, Cause: cause, Left: fmt.Errorf("could not delete %s: %w", plan.Name, err)}
	}
	return &RolledBack{Branch: plan.Name, Cause: cause}
}

func (s Service) switchBack(ctx context.Context, plan Plan) error {
	if plan.Return == "" {
		if err := s.Git.SwitchDetached(ctx, plan.At); err != nil {
			return fmt.Errorf("could not detach again at %s: %w", plan.At, err)
		}
		return nil
	}
	if err := s.Git.SwitchExisting(ctx, plan.Return); err != nil {
		return fmt.Errorf("could not switch back to %s: %w", plan.Return, err)
	}
	return nil
}

// RolledBack is a recording that failed after the branch was created. Left is
// what the rollback could not undo, nil when it undid everything.
type RolledBack struct {
	Branch string
	Cause  error
	Left   error
}

func (r *RolledBack) Error() string {
	if r.Left != nil {
		return fmt.Sprintf("recording %s failed (%v), and the rollback did not finish: %v", r.Branch, r.Cause, r.Left)
	}
	return fmt.Sprintf("recording %s failed, so it was removed and the checkout is back where it was: %v", r.Branch, r.Cause)
}
func (r *RolledBack) Unwrap() error { return r.Cause }

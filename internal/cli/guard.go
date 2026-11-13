package cli

import (
	"context"

	"github.com/shhac/g2g/internal/restack"
	"github.com/shhac/g2g/internal/stack"
)

// idleGuard refuses a mutation while a restack is unfinished.
//
// Two questions, named, rather than one function told apart by its argument:
// RequireIdle refuses while any restack is unfinished, which is every command
// that cannot say what it touches; RequireIdleFor refuses one touching a
// branch an unfinished restack moves, or running in the worktree it belongs
// to, and with nothing touched asks only the second.
type idleGuard interface {
	RequireIdle(ctx context.Context) error
	RequireIdleFor(ctx context.Context, touching []string) error
}

var _ idleGuard = restack.Service{}

// restackGuard refuses a mutation while a restack is unfinished. A service
// that was never configured guards nothing, which is what keeps a root command
// built without one working exactly as before.
func restackGuard(service restack.Service) idleGuard {
	if service.Journal == nil {
		return nil
	}
	return service
}

// guardBefore refuses, before anything is discovered, a command that cannot
// say which branches it touches, and any command in the worktree an unfinished
// restack belongs to: a half-rewritten checkout can fail discovery before the
// refusal that names the restack is reached.
func (f applyFlow[P]) guardBefore(ctx context.Context) error {
	if f.guard == nil {
		return nil
	}
	if f.touches == nil {
		return f.guard.RequireIdle(ctx)
	}
	return f.guard.RequireIdleFor(ctx, nil)
}

// guardAfter refuses a plan touching a branch an unfinished restack moves.
func (f applyFlow[P]) guardAfter(ctx context.Context, plan P) error {
	if f.guard == nil || f.touches == nil {
		return nil
	}
	return f.guard.RequireIdleFor(ctx, f.touches(plan))
}

// snapshotTouches is every branch a selection acts on and the parents it
// measures them against.
func snapshotTouches(snapshot stack.Snapshot) []string {
	touching := append([]string{snapshot.Base}, snapshot.Branches...)
	for _, parent := range snapshot.Parents {
		touching = append(touching, parent)
	}
	return touching
}

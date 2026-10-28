// Package pause is the one wait g2g makes: one that stops when its context
// does, behind a type a test can replace so a suite never spends the wall
// clock the real one would. land waited with its own, and keeping the stack
// comments came to need the same; it depends on nothing, so either may use it
// and a third copy has no reason to exist.
package pause

import (
	"context"
	"time"
)

// Func waits for d, or reports that the context gave up first.
type Func func(ctx context.Context, d time.Duration) error

// For is the real wait. It stops early when the context does, because a
// command's budget is the only thing bounding a wait, and a timer that ignored
// it would outlive the command.
func For(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Or is f, or For when f is nil: a service that leaves its clock unset waits
// for real.
func Or(f Func) Func {
	if f == nil {
		return For
	}
	return f
}

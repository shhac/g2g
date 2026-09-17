// Package parallel asks several independent questions of external commands at
// once, bounded.
//
// It depends on nothing, which is what lets internal/graph use it: that package
// may reach Git and no further, and a helper that arrived through another one
// would bring whatever that one reaches with it.
//
// It lived in internal/link, where the pattern was found. internal/graph then
// needed exactly the same thing, and a second copy of bounded concurrency is
// the kind of duplication this repository has been bitten by before — the
// copies drift, and the property that goes missing is never the obvious one.
package parallel

import (
	"context"
	"runtime"
	"sync"
)

// Readers bounds how many questions are in flight at once.
//
// Every question is one process, and the answers are independent: a fourteen-
// branch stack spent two thirds of a status waiting for them one at a time. The
// bound exists because the work is process spawns rather than arithmetic —
// unbounded, a wide repository would start a hundred of them.
func Readers() int {
	const most = 8
	if cpus := runtime.NumCPU(); cpus < most {
		return max(cpus, 1)
	}
	return most
}

// Each runs ask over every subject, several at a time, and returns the first
// error any of them gave.
//
// The context is cancelled as soon as one fails, so a wide fan-out whose first
// answer is a failure does not go on paying for the rest of them. ask must be
// safe to call from several goroutines: everything it may touch has to be
// either its own, or a distinct element of a slice sized before the asking
// begins. The index is passed for exactly that — it is what lets a caller write
// results without a lock.
func Each[T any](ctx context.Context, subjects []T, ask func(context.Context, int, T) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wait  sync.WaitGroup
		once  sync.Once
		first error
	)
	slots := make(chan struct{}, Readers())
	for index, subject := range subjects {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			wait.Wait()
			return firstOr(first, ctx.Err())
		}
		wait.Add(1)
		go func() {
			defer wait.Done()
			defer func() { <-slots }()
			if err := ask(ctx, index, subject); err != nil {
				once.Do(func() { first = err; cancel() })
			}
		}()
	}
	wait.Wait()
	return first
}

// firstOr prefers the error an ask gave over the cancellation it caused, so a
// caller is told what actually went wrong rather than that something was
// cancelled.
func firstOr(first, fallback error) error {
	if first != nil {
		return first
	}
	return fallback
}

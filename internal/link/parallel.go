package link

import (
	"context"
	"runtime"
	"sync"
)

// readers bounds how many branches are asked about at once.
//
// Every question here is one git process, and the answers are independent: a
// fourteen-branch stack spent two thirds of a status waiting for them one at a
// time. The bound exists because the work is process spawns rather than
// arithmetic — unbounded, a wide stack would start a hundred of them.
func readers() int {
	const most = 8
	if cpus := runtime.NumCPU(); cpus < most {
		return max(cpus, 1)
	}
	return most
}

// eachBranch runs read over every branch, several at a time, and returns the
// first error any of them gave.
//
// The context is cancelled as soon as one fails, so a wide stack whose first
// answer is a failure does not go on paying for the rest of them. read must be
// safe to call from several goroutines: everything it may touch here is either
// its own, or a distinct element of a slice sized before the reads begin.
func eachBranch(ctx context.Context, branches []string, read func(context.Context, int, string) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wait  sync.WaitGroup
		once  sync.Once
		first error
	)
	slots := make(chan struct{}, readers())
	for index, branch := range branches {
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
			if err := read(ctx, index, branch); err != nil {
				once.Do(func() { first = err; cancel() })
			}
		}()
	}
	wait.Wait()
	return first
}

// firstOr prefers the error a read gave over the cancellation it caused, so a
// caller is told what actually went wrong rather than that something was
// cancelled.
func firstOr(first, fallback error) error {
	if first != nil {
		return first
	}
	return fallback
}

package link

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func names(count int) []string {
	branches := make([]string, count)
	for index := range branches {
		branches[index] = "synthetic-" + string(rune('a'+index%26))
	}
	return branches
}

// Every question here is a process, so a wide stack must not start one per
// branch. The bound is the whole reason this is not a goroutine per item.
func TestReadsAreBounded(t *testing.T) {
	var running, peak atomic.Int64
	err := eachBranch(context.Background(), names(64), func(context.Context, int, string) error {
		now := running.Add(1)
		for {
			was := peak.Load()
			if now <= was || peak.CompareAndSwap(was, now) {
				break
			}
		}
		defer running.Add(-1)
		return nil
	})
	if err != nil {
		t.Fatalf("eachBranch() error = %v", err)
	}
	if peak.Load() > int64(readers()) {
		t.Errorf("%d reads ran at once, want at most %d", peak.Load(), readers())
	}
}

// Each read is given its own index, and writing through it is what lets the
// results be collected without a lock. If two ever shared one, this is where
// the race detector finds it.
func TestEachReadOwnsItsOwnIndex(t *testing.T) {
	branches := names(32)
	seen := make([]string, len(branches))
	if err := eachBranch(context.Background(), branches, func(_ context.Context, index int, branch string) error {
		seen[index] = branch
		return nil
	}); err != nil {
		t.Fatalf("eachBranch() error = %v", err)
	}
	for index, branch := range branches {
		if seen[index] != branch {
			t.Fatalf("read %d saw %q, want %q", index, seen[index], branch)
		}
	}
}

// A caller is told what went wrong, not that something was cancelled — the
// cancellation is this function's own doing.
func TestTheFailureIsReportedRatherThanTheCancellationItCauses(t *testing.T) {
	wanted := errors.New("synthetic git failure")
	var mu sync.Mutex
	cancelled := 0

	err := eachBranch(context.Background(), names(32), func(ctx context.Context, index int, _ string) error {
		if index == 0 {
			return wanted
		}
		<-ctx.Done()
		mu.Lock()
		cancelled++
		mu.Unlock()
		return ctx.Err()
	})

	if !errors.Is(err, wanted) {
		t.Errorf("error = %v, want %v", err, wanted)
	}
	if cancelled == 0 {
		t.Error("the remaining reads were not told to stop, so a wide stack keeps paying after the first failure")
	}
}

// Nothing to read is not an error, and must not deadlock waiting for a worker
// that was never started.
func TestNoBranchesIsNotAnError(t *testing.T) {
	if err := eachBranch(context.Background(), nil, func(context.Context, int, string) error {
		t.Error("a read ran with no branches")
		return nil
	}); err != nil {
		t.Errorf("eachBranch() error = %v", err)
	}
}

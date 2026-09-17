package parallel

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// This is a shared seam now — internal/link and internal/graph both fan out
// through it — so it is tested where it lives rather than only through
// whichever caller happened to have a test.

// The bound is the point, in both directions: several at once, and not all of
// them, because the work is process spawns and unbounded a wide repository
// would start one per branch.
//
// Each reader announces itself and then waits, so the overlap is made to happen
// rather than hoped for. Counting how many merely happened to coincide is a
// test that passes on a busy machine and fails on an idle one, where the work
// finishes before the next goroutine is scheduled.
func TestEachRunsSeveralAtOnceButNotAllOfThem(t *testing.T) {
	subjects := make([]int, 64)
	arrived := make(chan struct{}, len(subjects))
	release := make(chan struct{})
	var running, peak atomic.Int64

	go func() {
		defer close(release)
		for range Readers() {
			select {
			case <-arrived:
			case <-time.After(5 * time.Second):
				// Fewer than the bound ever ran together. Releasing lets Each
				// finish so the assertion below reports it.
				return
			}
		}
	}()

	err := Each(context.Background(), subjects, func(context.Context, int, int) error {
		now := running.Add(1)
		defer running.Add(-1)
		for {
			was := peak.Load()
			if now <= was || peak.CompareAndSwap(was, now) {
				break
			}
		}
		arrived <- struct{}{}
		<-release
		return nil
	})

	if err != nil {
		t.Fatalf("Each() error = %v", err)
	}
	if peak.Load() > int64(Readers()) {
		t.Errorf("%d ran at once, want at most %d", peak.Load(), Readers())
	}
	if peak.Load() < int64(Readers()) {
		t.Errorf("%d ran at once, want the full bound of %d", peak.Load(), Readers())
	}
}

// Every subject gets its own index, which is what lets a caller write results
// without a lock.
func TestEachGivesEverySubjectItsOwnIndex(t *testing.T) {
	subjects := []string{"synthetic-a", "synthetic-b", "synthetic-c", "synthetic-d"}
	seen := make([]string, len(subjects))

	if err := Each(context.Background(), subjects, func(_ context.Context, index int, subject string) error {
		seen[index] = subject
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if strings.Join(seen, ",") != strings.Join(subjects, ",") {
		t.Errorf("seen = %v, want each subject at its own index", seen)
	}
}

// A wide fan-out whose first answer is a failure should not go on paying for
// the rest of them.
func TestEachStopsAskingOnceOneFails(t *testing.T) {
	refused := errors.New("synthetic refusal")
	var asked atomic.Int64
	subjects := make([]int, 500)

	err := Each(context.Background(), subjects, func(ctx context.Context, index int, _ int) error {
		asked.Add(1)
		if index == 0 {
			return refused
		}
		<-ctx.Done()
		return ctx.Err()
	})

	// The refusal, never the cancellation it caused: a reader can only see the
	// context end after the failing one has already recorded why, so what
	// comes back is what actually went wrong. That ordering is the whole
	// reason firstOr prefers a recorded error, and it is what a caller needs —
	// "context canceled" names nothing anybody can act on.
	if !errors.Is(err, refused) {
		t.Fatalf("Each() error = %v, want the refusal rather than the cancellation it caused", err)
	}
	if errors.Is(err, context.Canceled) {
		t.Errorf("Each() reported its own cancellation: %v", err)
	}
	if asked.Load() == int64(len(subjects)) {
		t.Error("every subject was asked despite the first one failing")
	}
}

// A context that is already done must not buy a round of work.
func TestEachRespectsAContextThatHasAlreadyExpired(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var asked atomic.Int64

	err := Each(ctx, make([]int, 200), func(context.Context, int, int) error {
		asked.Add(1)
		return nil
	})

	if err == nil {
		t.Fatal("Each() error = nil, want the context's own")
	}
	if asked.Load() == int64(200) {
		t.Errorf("asked %d subjects under a cancelled context", asked.Load())
	}
}

func TestEachOnNothingDoesNothing(t *testing.T) {
	if err := Each(context.Background(), []string{}, func(context.Context, int, string) error {
		t.Error("asked about a subject that was not there")
		return nil
	}); err != nil {
		t.Errorf("Each() error = %v", err)
	}
}

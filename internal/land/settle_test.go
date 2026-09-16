package land

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shhac/g2g/internal/diagnostic"
)

// recordingPauser stands in for the clock. It records what it was asked to
// wait, and can refuse after a given number of waits to stand in for a
// context that has run out.
type recordingPauser struct {
	waited []time.Duration
	giveUp int
}

func (p *recordingPauser) wait(_ context.Context, interval time.Duration) error {
	p.waited = append(p.waited, interval)
	if p.giveUp != 0 && len(p.waited) >= p.giveUp {
		return context.DeadlineExceeded
	}
	return nil
}

// The ordinary case is that GitHub already agrees: it has had a moment to see
// a push, and it performed the merge itself. Sleeping first would add the
// whole interval to every branch in the stack for nothing.
func TestSettleAsksBeforeItWaits(t *testing.T) {
	clock := &recordingPauser{}
	asked := 0

	err := settle(context.Background(), clock.wait, "the push", func(context.Context) (bool, error) {
		asked++
		return true, nil
	})

	if err != nil {
		t.Fatalf("settle() error = %v", err)
	}
	if asked != 1 {
		t.Errorf("asked %d times, want once", asked)
	}
	if len(clock.waited) != 0 {
		t.Errorf("waited %v, want not at all", clock.waited)
	}
}

func TestSettleKeepsAskingUntilTheAnswerIsYes(t *testing.T) {
	clock := &recordingPauser{}
	asked := 0

	err := settle(context.Background(), clock.wait, "the push", func(context.Context) (bool, error) {
		asked++
		return asked == 3, nil
	})

	if err != nil {
		t.Fatalf("settle() error = %v", err)
	}
	if asked != 3 {
		t.Errorf("asked %d times, want three", asked)
	}
	// Backing off matters: GitHub computing mergeability is not helped by
	// being asked twice a second.
	if len(clock.waited) != 2 || clock.waited[0] != settleFirst || clock.waited[1] != settleFirst*2 {
		t.Errorf("waited %v, want a widening gap from %v", clock.waited, settleFirst)
	}
}

func TestSettleBacksOffNoFurtherThanItsCeiling(t *testing.T) {
	clock := &recordingPauser{}
	asked := 0

	if err := settle(context.Background(), clock.wait, "the merge", func(context.Context) (bool, error) {
		asked++
		return asked == 8, nil
	}); err != nil {
		t.Fatalf("settle() error = %v", err)
	}

	for _, waited := range clock.waited {
		if waited > settleMost {
			t.Fatalf("waited %v, want nothing above %v", clock.waited, settleMost)
		}
	}
	if last := clock.waited[len(clock.waited)-1]; last != settleMost {
		t.Errorf("final wait = %v, want the ceiling %v", last, settleMost)
	}
}

// A wait that runs out has to say which wait it was: a push GitHub never
// registered and a merge that never reached the base fail for different
// reasons and want different responses.
func TestSettleNamesWhatItGaveUpWaitingFor(t *testing.T) {
	clock := &recordingPauser{giveUp: 3}

	err := settle(context.Background(), clock.wait, "the merge to reach synthetic-main", func(context.Context) (bool, error) {
		return false, nil
	})

	var notSettled *NotSettled
	if !errors.As(err, &notSettled) {
		t.Fatalf("settle() error = %v, want a *NotSettled", err)
	}
	if notSettled.What != "the merge to reach synthetic-main" {
		t.Errorf("What = %q", notSettled.What)
	}
	if notSettled.Attempts != 3 {
		t.Errorf("Attempts = %d, want the three it made", notSettled.Attempts)
	}
	for _, want := range []string{"the merge to reach synthetic-main", "3 attempts"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

// Asking is a GitHub call, and a failure to ask is not a failure to settle.
func TestSettleReturnsTheAskFailureUnchanged(t *testing.T) {
	clock := &recordingPauser{}
	refused := errors.New("synthetic gh failure")

	err := settle(context.Background(), clock.wait, "the push", func(context.Context) (bool, error) {
		return false, refused
	})

	if !errors.Is(err, refused) {
		t.Fatalf("settle() error = %v, want the ask's own failure", err)
	}
	var notSettled *NotSettled
	if errors.As(err, &notSettled) {
		t.Error("a failed ask was reported as a wait that ran out")
	}
}

func TestSettleReportsEachAttemptUnderDebug(t *testing.T) {
	var diagnostics bytes.Buffer
	ctx := diagnostic.WithSink(context.Background(), diagnostic.Writer{Out: &diagnostics})
	clock := &recordingPauser{}
	asked := 0

	if err := settle(ctx, clock.wait, "the push", func(context.Context) (bool, error) {
		asked++
		return asked == 2, nil
	}); err != nil {
		t.Fatal(err)
	}

	got := diagnostics.String()
	for _, want := range []string{"event=land.settle", `what="the push"`, `attempt="1"`, `settled="false"`, `attempt="2"`, `settled="true"`} {
		if !strings.Contains(got, want) {
			t.Errorf("diagnostics missing %q: %q", want, got)
		}
	}
}

// The real pauser is what bounds a settle against the mutation budget, so a
// context that is already done must not buy another interval.
func TestThePauserStopsWhenTheContextDoes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	started := time.Now()
	if err := pause(ctx, time.Hour); err == nil {
		t.Fatal("pause() error = nil, want the context's own")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Errorf("pause() took %v, want to stop with the context", elapsed)
	}
}

package settle

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/githubstack"
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

	err := Until(context.Background(), clock.wait, Question{Event: "test.settle", What: "the push", Rerun: "g2g land"}, func(context.Context) (bool, error) {
		asked++
		return true, nil
	})

	if err != nil {
		t.Fatalf("Until() error = %v", err)
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

	err := Until(context.Background(), clock.wait, Question{Event: "test.settle", What: "the push", Rerun: "g2g land"}, func(context.Context) (bool, error) {
		asked++
		return asked == 3, nil
	})

	if err != nil {
		t.Fatalf("Until() error = %v", err)
	}
	if asked != 3 {
		t.Errorf("asked %d times, want three", asked)
	}
	// Backing off matters: GitHub computing mergeability is not helped by
	// being asked twice a second.
	if len(clock.waited) != 2 || clock.waited[0] != First || clock.waited[1] != First*2 {
		t.Errorf("waited %v, want a widening gap from %v", clock.waited, First)
	}
}

func TestSettleBacksOffNoFurtherThanItsCeiling(t *testing.T) {
	clock := &recordingPauser{}
	asked := 0

	if err := Until(context.Background(), clock.wait, Question{Event: "test.settle", What: "the merge", Rerun: "g2g land"}, func(context.Context) (bool, error) {
		asked++
		return asked == 8, nil
	}); err != nil {
		t.Fatalf("Until() error = %v", err)
	}

	for _, waited := range clock.waited {
		if waited > most {
			t.Fatalf("waited %v, want nothing above %v", clock.waited, most)
		}
	}
	if last := clock.waited[len(clock.waited)-1]; last != most {
		t.Errorf("final wait = %v, want the ceiling %v", last, most)
	}
}

// A wait that runs out has to say which wait it was: a push GitHub never
// registered and a merge that never reached the base fail for different
// reasons and want different responses.
func TestSettleNamesWhatItGaveUpWaitingFor(t *testing.T) {
	clock := &recordingPauser{giveUp: 3}

	err := Until(context.Background(), clock.wait, Question{Event: "test.settle", What: "the merge to reach synthetic-main", Rerun: "g2g land"}, func(context.Context) (bool, error) {
		return false, nil
	})

	var notSettled *NotSettled
	if !errors.As(err, &notSettled) {
		t.Fatalf("Until() error = %v, want a *NotSettled", err)
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

	err := Until(context.Background(), clock.wait, Question{Event: "test.settle", What: "the push", Rerun: "g2g land"}, func(context.Context) (bool, error) {
		return false, refused
	})

	if !errors.Is(err, refused) {
		t.Fatalf("Until() error = %v, want the ask's own failure", err)
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

	if err := Until(ctx, clock.wait, Question{Event: "test.settle", What: "the push", Rerun: "g2g land"}, func(context.Context) (bool, error) {
		asked++
		return asked == 2, nil
	}); err != nil {
		t.Fatal(err)
	}

	got := diagnostics.String()
	for _, want := range []string{"event=test.settle", `what="the push"`, `attempt="1"`, `settled="false"`, `attempt="2"`, `settled="true"`} {
		if !strings.Contains(got, want) {
			t.Errorf("diagnostics missing %q: %q", want, got)
		}
	}
}

// A wait that has gone on for a minute is asked less often: still widening
// from the ceiling a young wait keeps to, never past the slow one.
func TestSettleSlowsDownOnceAWaitHasGoneOnAMinute(t *testing.T) {
	clock := &recordingPauser{}
	asked := 0
	if err := Until(context.Background(), clock.wait, Question{Event: "test.settle", What: "the merge", Rerun: "g2g land"}, func(context.Context) (bool, error) {
		asked++
		return asked == 20, nil
	}); err != nil {
		t.Fatalf("Until() error = %v", err)
	}
	var total time.Duration
	for _, waited := range clock.waited {
		if total < slow && waited > most {
			t.Fatalf("waited %v, want nothing above %v in the first %v", clock.waited, most, slow)
		}
		if waited > late {
			t.Fatalf("waited %v, want nothing above %v", clock.waited, late)
		}
		total += waited
	}
	if last := clock.waited[len(clock.waited)-1]; last != late {
		t.Errorf("final wait = %v, want the slow ceiling %v", last, late)
	}
}

// GitHub refusing for being asked too often is waited out at the slow pace,
// and named if the wait runs out while it still is.
func TestSettleWaitsOutARateLimit(t *testing.T) {
	limited := &githubstack.CommandError{Command: "gh api graphql", Cause: errors.New("synthetic exit 1"), Output: `{"errors":[{"type":"RATE_LIMITED","message":"API rate limit exceeded for user ID 1."}]}`}
	clock := &recordingPauser{}
	asked := 0
	if err := Until(context.Background(), clock.wait, Question{Event: "test.settle", What: "the merge", Rerun: "g2g land"}, func(context.Context) (bool, error) {
		asked++
		if asked == 1 {
			return false, limited
		}
		return true, nil
	}); err != nil {
		t.Fatalf("Until() error = %v, want the limit waited out", err)
	}
	if len(clock.waited) != 1 || clock.waited[0] != late {
		t.Errorf("waited %v, want one wait at %v", clock.waited, late)
	}

	gaveUp := &recordingPauser{giveUp: 1}
	err := Until(context.Background(), gaveUp.wait, Question{Event: "test.settle", What: "the merge", Rerun: "g2g land"}, func(context.Context) (bool, error) { return false, limited })
	if err == nil || !strings.Contains(err.Error(), "too often") || !errors.Is(err, limited) {
		t.Errorf("Until() error = %v, want the rate limit named", err)
	}
}

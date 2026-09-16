// Package land takes a stack down onto its trunk, one branch at a time.
//
// It owns no rules of its own. Publishing, advancing the base, replaying and
// forgetting are each a service that already exists, already previews, and
// already refuses the things it knows how to refuse; this decides only the
// order, waits for GitHub in the two places where acting on a stale answer
// would merge the wrong thing, and rewrites one edge per branch that lands.
package land

import (
	"context"
	"fmt"
	"time"

	"github.com/shhac/g2g/internal/diagnostic"
)

// The wait between attempts, and its ceiling. Short enough that an answer
// GitHub already has is not sat on, long enough that a slow one is not asked
// fifty times.
const (
	settleFirst = 2 * time.Second
	settleMost  = 8 * time.Second
)

// pauser waits, or reports that the context gave up first.
//
// It is a parameter rather than a call to time.Sleep so that a test can drive
// the loop without spending the wall clock the real one does. This is the
// first thing in g2g that waits for anything, and a test that genuinely slept
// would be the reason nobody ran it.
type pauser func(context.Context, time.Duration) error

// pause is the real wait. It stops early when the context does, because the
// mutation budget is the only thing bounding a settle and a timer that
// ignored it would outlive the command.
func pause(ctx context.Context, wait time.Duration) error {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// NotSettled reports a wait that ran out before GitHub agreed.
//
// It names what was being waited for rather than saying "timed out", because
// the two waits fail for entirely different reasons and want different
// responses: a push GitHub never registered is worth retrying, and a merge
// that never reached the base usually means the branch is queued rather than
// merged.
type NotSettled struct {
	What     string
	Attempts int
}

func (e *NotSettled) Error() string {
	return fmt.Sprintf("gave up waiting for %s after %d %s", e.What, e.Attempts, attemptWord(e.Attempts))
}

func attemptWord(attempts int) string {
	if attempts == 1 {
		return "attempt"
	}
	return "attempts"
}

// settle asks until the answer is yes, the context gives up, or asking fails.
//
// It asks before it waits. The ordinary case is that GitHub already agrees --
// a push it has had a moment to see, a merge it performed itself -- and a loop
// that slept first would add its whole interval to every branch in the stack
// for nothing.
func settle(ctx context.Context, wait pauser, what string, ask func(context.Context) (bool, error)) error {
	if wait == nil {
		wait = pause
	}
	interval := settleFirst
	for attempts := 1; ; attempts++ {
		settled, err := ask(ctx)
		if err != nil {
			return err
		}
		diagnostic.Event(ctx, "land.settle",
			diagnostic.Field{Key: "what", Value: what},
			diagnostic.Field{Key: "attempt", Value: fmt.Sprint(attempts)},
			diagnostic.Field{Key: "settled", Value: fmt.Sprintf("%t", settled)},
		)
		if settled {
			return nil
		}
		if err := wait(ctx, interval); err != nil {
			// The context expiring here is the wait running out, not a
			// failure of its own, and saying which wait it was is the whole
			// point of having two.
			return &NotSettled{What: what, Attempts: attempts}
		}
		if interval < settleMost {
			interval *= 2
			if interval > settleMost {
				interval = settleMost
			}
		}
	}
}

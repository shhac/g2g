// Package settle waits for GitHub to agree with something already done: a
// push it has not yet registered, a merge it has not yet reported.
//
// land waited this way and nothing else did, so submit returned the moment
// its push finished and a gh pr ready straight after it read the previous
// head. One loop, because the pacing, the rate-limit handling and the
// diagnostic event are each a decision that was made once and should not be
// made again by a copy.
package settle

import (
	"context"
	"fmt"
	"time"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/pause"
	"github.com/shhac/g2g/internal/words"
)

// Question is what a wait is for, as each place it is said needs it: the
// diagnostic event it is reported under, what it is waiting for in words, and
// the command to rerun once GitHub stops refusing requests.
type Question struct {
	Event, What, Rerun string
}

// The wait between attempts, and its ceiling. Short enough that an answer
// GitHub already has is not sat on, long enough that a slow one is not asked
// fifty times.
//
// A wait that has gone on for a minute is not going to answer in seconds --
// a pull request in a merge queue, checks that take a while -- so after
// slow the gap keeps widening, to late. Asked every eight seconds
// for the length of a descent's budget, one stuck merge was a few hundred
// requests against the same allowance every other gh call draws on.
const (
	First = 2 * time.Second
	most  = 8 * time.Second
	slow  = time.Minute
	late  = 30 * time.Second
)

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
	return fmt.Sprintf("gave up waiting for %s after %d %s", e.What, e.Attempts, words.Pick(e.Attempts, "attempt", "attempts"))
}

// Until asks until the answer is yes, the context gives up, or asking fails.
//
// It asks before it waits. The ordinary case is that GitHub already agrees --
// a push it has had a moment to see, a merge it performed itself -- and a loop
// that slept first would add its whole interval to every branch in the stack
// for nothing.
func Until(ctx context.Context, wait pause.Func, question Question, ask func(context.Context) (bool, error)) error {
	what := question.What
	wait = pause.Or(wait)
	interval, waited := First, time.Duration(0)
	for attempts := 1; ; attempts++ {
		settled, err := ask(ctx)
		// GitHub refusing for being asked too often is one more thing to wait
		// out, at the slow pace; stopping part-way down a stack for it would
		// leave a descent half done over something that passes with time.
		limited := githubstack.RateLimited(err)
		if err != nil && !limited {
			return err
		}
		diagnostic.Event(ctx, question.Event,
			diagnostic.Field{Key: "what", Value: what},
			diagnostic.Field{Key: "attempt", Value: fmt.Sprint(attempts)},
			diagnostic.Field{Key: "settled", Value: fmt.Sprintf("%t", settled)},
			diagnostic.Field{Key: "rate_limited", Value: fmt.Sprintf("%t", limited)},
		)
		if settled {
			return nil
		}
		pace := interval
		if limited {
			pace = max(interval, late)
		}
		if waitErr := wait(ctx, pace); waitErr != nil {
			if limited {
				return fmt.Errorf("GitHub was still refusing requests for coming too often when the wait for %s ran out · rerun %s once its limit resets: %w", what, question.Rerun, err)
			}
			// The context expiring here is the wait running out, not a
			// failure of its own, and saying which wait it was is the whole
			// point of having two.
			return &NotSettled{What: what, Attempts: attempts}
		}
		waited += pace
		interval = nextInterval(interval, waited)
	}
}

// nextInterval widens the gap between attempts: to most while the wait
// is young, and on to late once it has gone on for slow. Counted
// in time waited rather than read from the clock, so a test that does not
// sleep sees the same sequence the real wait does.
func nextInterval(interval, waited time.Duration) time.Duration {
	if waited < slow {
		return min(interval*2, most)
	}
	return min(interval*2, late)
}

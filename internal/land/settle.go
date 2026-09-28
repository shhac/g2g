package land

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/shhac/g2g/internal/diagnostic"
	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/githubstack"
)

// The wait between attempts, and its ceiling. Short enough that an answer
// GitHub already has is not sat on, long enough that a slow one is not asked
// fifty times.
const (
	settleFirst = 2 * time.Second
	settleMost  = 8 * time.Second
)

// diagnoseBudget bounds the one read made after a wait has given up, which
// runs past the mutation budget by design: that budget expiring is what ended
// the wait, and the read is what says why.
const diagnoseBudget = 10 * time.Second

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
	return fmt.Sprintf("gave up waiting for %s after %d %s", e.What, e.Attempts, pick(e.Attempts, "attempt", "attempts"))
}

// pick is singular for one and plural otherwise.
func pick(count int, singular, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
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
		interval = min(interval*2, settleMost)
	}
}

// settlePush waits until GitHub is looking at what was just pushed.
//
// Three facts, not one. headRefOid says GitHub has the commit; baseRefName
// says the merge will go where it is meant to; mergeable says GitHub has
// finished working out whether it can merge at all, which it reports as
// UNKNOWN while it thinks. None of them is about CI.
func (s Service) settlePush(ctx context.Context, step Step, tip string, remote string) error {
	err := settle(ctx, s.pause, fmt.Sprintf("GitHub to see %s at %s", step.Branch, shortID(tip)), func(ctx context.Context) (bool, error) {
		state, err := s.state(ctx, step.Number)
		if err != nil {
			return false, err
		}
		return state.HeadOID == tip && state.Base == step.Base && state.Mergeable != githubstack.MergeableUnknown, nil
	})
	var unsettled *NotSettled
	if !errors.As(err, &unsettled) {
		return err
	}
	// Which of the two failed. "Waiting for GitHub" reads as propagation lag,
	// and the answer to that is to wait longer -- so a push that never reached
	// the remote at all sends somebody to raise a timeout that was never the
	// problem. The remote is one cheap read and it separates them.
	//
	// Asked on a context of its own, because the wait only gives up once the
	// budget has expired, and a read on that context never runs: the diagnosis
	// existed and could not happen, which only a fake that ignored the context
	// failed to notice. Bounded, so an unreachable remote cannot hold a command
	// that has already run out of time.
	diagnose, cancel := context.WithTimeout(context.WithoutCancel(ctx), diagnoseBudget)
	defer cancel()
	tips, readErr := s.Git.RemoteTips(diagnose, remote, []string{step.Branch})
	if readErr != nil {
		return err
	}
	if published, present := tips[step.Branch]; !present || published != tip {
		return fmt.Errorf("%s is not on %s at %s (it has %s), so GitHub was never going to see it · the push did not take effect",
			step.Branch, remote, shortID(tip), describeTip(published))
	}
	return err
}

// describeTip names a tip a remote does not have, so the sentence above reads
// as a fact either way.
func describeTip(tip string) string {
	if tip == "" {
		return "no such branch"
	}
	return shortID(tip)
}

// settleMerge waits until the merge has reached the base on the remote.
//
// By ancestry, never by watching the tip change: a colleague's unrelated push
// changes it too, and acting on that fetches a base that does not contain this
// merge and replays the branch above onto it. The merge commit is already in
// the answer GitHub gives about the merge it just performed.
func (s Service) settleMerge(ctx context.Context, plan Plan, step Step) error {
	return settle(ctx, s.pause, fmt.Sprintf("the merge of %s to reach %s", step.Branch, plan.Trunk), func(ctx context.Context) (bool, error) {
		state, err := s.state(ctx, step.Number)
		if err != nil {
			return false, err
		}
		if !state.Merged() || state.MergeCommit == "" {
			return false, nil
		}
		if err := s.Git.FetchIsolated(ctx, plan.Options.Remote, []string{plan.Trunk}); err != nil {
			return false, err
		}
		return s.Git.IsAncestor(ctx, state.MergeCommit, localgit.IsolatedRef(plan.Options.Remote, plan.Trunk))
	})
}

func (s Service) state(ctx context.Context, number int) (githubstack.MergeState, error) {
	mergeability, err := s.GitHub.Mergeability(ctx, []int{number})
	if err != nil {
		return githubstack.MergeState{}, err
	}
	return mergeability.States[number], nil
}

func shortID(object string) string {
	if len(object) <= 7 {
		return object
	}
	return object[:7]
}

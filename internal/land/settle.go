package land

import (
	"context"
	"errors"
	"fmt"
	"time"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/settle"
)

// diagnoseBudget bounds the one read made after a wait has given up, which
// runs past the mutation budget by design: that budget expiring is what ended
// the wait, and the read is what says why.
const diagnoseBudget = 10 * time.Second

// until is settle.Until as a descent asks it.
func (s Service) until(ctx context.Context, what string, ask func(context.Context) (bool, error)) error {
	return settle.Until(ctx, s.pause, settle.Question{Event: "land.settle", What: what, Rerun: "g2g land"}, ask)
}

// settlePush waits until GitHub is looking at what was just pushed, and
// answers with the merge state that said so.
//
// Three facts, not one. headRefOid says GitHub has the commit; baseRefName
// says the merge will go where it is meant to; mergeable says GitHub has
// finished working out whether it can merge at all, which it reports as
// UNKNOWN while it thinks. None of them is about CI.
func (s Service) settlePush(ctx context.Context, step Step, tip string, remote string) (githubstack.MergeState, error) {
	var settled githubstack.MergeState
	err := s.until(ctx, fmt.Sprintf("GitHub to see %s at %s", step.Branch, localgit.Short(tip)), func(ctx context.Context) (bool, error) {
		state, err := s.state(ctx, step.Number)
		if err != nil {
			return false, err
		}
		settled = state
		return state.HeadOID == tip && state.Base == step.Base && state.Mergeable != githubstack.MergeableUnknown, nil
	})
	var unsettled *settle.NotSettled
	if !errors.As(err, &unsettled) {
		return settled, err
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
		return settled, err
	}
	if published, present := tips[step.Branch]; !present || published != tip {
		return settled, fmt.Errorf("%s is not on %s at %s (it has %s), so GitHub was never going to see it · the push did not take effect",
			step.Branch, remote, localgit.Short(tip), describeTip(published))
	}
	return settled, err
}

// describeTip names a tip a remote does not have, so the sentence above reads
// as a fact either way.
func describeTip(tip string) string {
	if tip == "" {
		return "no such branch"
	}
	return localgit.Short(tip)
}

// settleMergeObserved waits until the merge has reached the base on the remote.
//
// By ancestry, never by watching the tip change: a colleague's unrelated push
// changes it too, and acting on that fetches a base that does not contain this
// merge and replays the branch above onto it. The merge commit is already in
// the answer GitHub gives about the merge it just performed.
func (s Service) settleMergeObserved(ctx context.Context, plan Plan, step Step, observed *bool) error {
	return s.until(ctx, fmt.Sprintf("the merge of %s to reach %s", step.Branch, plan.Trunk), func(ctx context.Context) (bool, error) {
		state, err := s.state(ctx, step.Number)
		if err != nil {
			return false, err
		}
		if state.Merged() {
			*observed = true
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

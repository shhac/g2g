package submit

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/settle"
	"github.com/shhac/g2g/internal/words"
)

// settleBudget bounds the wait for GitHub to show what was pushed. GitHub
// usually reports a new head within seconds; past this, saying so is more use
// than waiting on.
const settleBudget = time.Minute

// settleHeads waits until every existing pull request this submission pushed
// to reports the head it pushed.
//
// A push finishing is not GitHub having seen it: the pull request goes on
// reporting the previous head for a moment, and the first thing done after a
// submit is often to mark the pull request ready, which a workflow then read
// at that previous head. Waiting here means a submit that returns has been
// seen. It cannot promise what an event GitHub raises later carries, and
// does not claim to.
//
// Only an existing pull request is asked: one this run opened was opened at
// the head just pushed. A reader that cannot say where a branch is, or a
// pull request whose head was never reported, waits for nothing, as submit
// did before.
func (s Service) settleHeads(ctx context.Context, plan Plan) error {
	want, err := s.pushedHeads(ctx, plan)
	if err != nil || len(want) == 0 {
		return err
	}
	branches := slices.Sorted(maps.Keys(want))
	ctx, cancel := context.WithTimeout(ctx, settleBudget)
	defer cancel()
	lagging := branches
	err = settle.Until(ctx, s.pause, settle.Question{
		Event: "submit.settle",
		What:  "GitHub to show " + words.Pick(len(branches), "the pull request", "the pull requests") + " at the pushed " + words.Pick(len(branches), "head", "heads"),
		Rerun: "g2g github status",
	}, func(ctx context.Context) (bool, error) {
		prs, err := s.GitHub.Inspect(ctx, branches)
		if err != nil {
			return false, err
		}
		lagging = behindOn(prs, want)
		return len(lagging) == 0, nil
	})
	if err == nil {
		return nil
	}
	return fmt.Errorf("published, but GitHub still shows the previous head for %s · it usually catches up within a minute; check with g2g github status before marking %s ready: %w",
		words.List(lagging), words.Pick(len(lagging), "it", "them"), err)
}

// pushedHeads is each branch with an existing open pull request that this
// submission published, at the tip it published.
func (s Service) pushedHeads(ctx context.Context, plan Plan) (map[string]string, error) {
	resolver, ok := s.Git.(interface {
		Resolve(context.Context, string) (string, error)
	})
	if !ok {
		return nil, nil
	}
	want := map[string]string{}
	for step := range githubstack.Along(plan.Snapshot.Base, plan.Snapshot.Branches, plan.Existing) {
		// A head GitHub did not report is one there is nothing to compare
		// with, which is no reason to wait.
		if step.Resolution.Open == nil || step.Resolution.Open.HeadOID == "" || plan.Push.Publishing[step.Branch].UpToDate() {
			continue
		}
		tip, err := resolver.Resolve(ctx, step.Branch)
		if err != nil {
			return nil, err
		}
		if step.Resolution.Open.HeadOID != tip {
			want[step.Branch] = tip
		}
	}
	return want, nil
}

// behindOn is the branches whose open pull request does not yet report the
// head wanted for it, in order.
func behindOn(prs []githubstack.PullRequest, want map[string]string) []string {
	behind := []string{}
	for branch, tip := range want {
		if !slices.ContainsFunc(prs, func(pr githubstack.PullRequest) bool {
			return pr.Head == branch && pr.State == "OPEN" && pr.HeadOID == tip
		}) {
			behind = append(behind, branch)
		}
	}
	slices.Sort(behind)
	return behind
}

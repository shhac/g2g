package submit

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/push"
	"github.com/shhac/g2g/internal/stack"
)

// resolvingGit is the fake Git that can say where each branch is, which is
// what the wait after a push compares GitHub's answer with.
type resolvingGit struct {
	*fakeGit
	tips map[string]string
}

func (g resolvingGit) Resolve(_ context.Context, branch string) (string, error) {
	return g.tips[branch], nil
}

// laggingGitHub reports a pull request at its previous head for a number of
// reads after the push, as GitHub does for a moment.
type laggingGitHub struct {
	fakeGitHub
	stale    int
	previous string
	pushed   string
	reads    int
}

func (g *laggingGitHub) Inspect(context.Context, []string) ([]githubstack.PullRequest, error) {
	g.reads++
	head := g.pushed
	if g.reads <= g.stale {
		head = g.previous
	}
	return []githubstack.PullRequest{{Head: "synthetic/lower", Base: "main", State: "OPEN", Number: 11, HeadOID: head}}, nil
}

func noWait(context.Context, time.Duration) error { return nil }

func settlingPlan() Plan {
	return Plan{
		Snapshot: stack.Snapshot{Base: "main", Branches: []string{"synthetic/lower"}},
		Remote:   "origin",
		Existing: []githubstack.PullRequest{{Head: "synthetic/lower", Base: "main", State: "OPEN", Number: 11, HeadOID: "synthetic-previous"}},
		Push: push.Plan{
			Snapshot:   stack.Snapshot{Base: "main", Branches: []string{"synthetic/lower"}},
			Publishing: map[string]push.Publication{"synthetic/lower": {Standing: push.Ahead, Ours: 1}},
		},
	}
}

// Marking a pull request ready straight after a submit started a workflow on
// the head from before the push: the submit had returned the moment its push
// did, while GitHub still reported the previous head.
func TestApplyWaitsForGitHubToShowThePushedHead(t *testing.T) {
	git := resolvingGit{&fakeGit{}, map[string]string{"synthetic/lower": "synthetic-pushed"}}
	github := &laggingGitHub{stale: 2, previous: "synthetic-previous", pushed: "synthetic-pushed"}
	service := Service{Git: git, GitHub: github, Pusher: git.fakeGit, pause: noWait}
	if err := service.Apply(context.Background(), settlingPlan(), Spec{Version: 1}, false); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if github.reads != 3 {
		t.Errorf("GitHub was read %d times, want until it showed the pushed head (3)", github.reads)
	}
}

// A GitHub that never catches up is reported as published and not yet seen,
// which is a stop part-way: the push stands.
func TestApplyStopsPartWayWhenGitHubNeverShowsThePushedHead(t *testing.T) {
	git := resolvingGit{&fakeGit{}, map[string]string{"synthetic/lower": "synthetic-pushed"}}
	github := &laggingGitHub{stale: 1 << 30, previous: "synthetic-previous", pushed: "synthetic-pushed"}
	gaveUp := func(context.Context, time.Duration) error { return context.DeadlineExceeded }
	service := Service{Git: git, GitHub: github, Pusher: git.fakeGit, pause: gaveUp}
	err := service.Apply(context.Background(), settlingPlan(), Spec{Version: 1}, false)
	var stopped *Stopped
	if !errors.As(err, &stopped) || !stopped.Pushed {
		t.Fatalf("Apply() error = %#v, want a stop after publishing", err)
	}
	if !strings.Contains(err.Error(), "GitHub still shows the previous head for synthetic/lower") {
		t.Errorf("the stop does not say what GitHub still shows: %v", err)
	}
}

// Nothing was pushed to an existing pull request, so there is nothing to wait
// for and GitHub is not asked.
func TestApplyDoesNotWaitWhenNothingWasPushedToAPullRequest(t *testing.T) {
	git := resolvingGit{&fakeGit{}, map[string]string{"synthetic/lower": "synthetic-previous"}}
	github := &laggingGitHub{previous: "synthetic-previous", pushed: "synthetic-previous"}
	plan := settlingPlan()
	plan.Push.Publishing["synthetic/lower"] = push.Publication{Standing: push.Current}
	service := Service{Git: git, GitHub: github, Pusher: git.fakeGit, pause: noWait}
	if err := service.Apply(context.Background(), plan, Spec{Version: 1}, false); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if github.reads != 0 {
		t.Errorf("GitHub was read %d times with nothing pushed to a pull request", github.reads)
	}
}

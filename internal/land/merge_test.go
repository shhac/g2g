package land

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shhac/g2g/internal/githubstack"
)

type retryGitHub struct {
	*fakeGitHub
	calls        int
	refuse       int
	afterRefusal func()
	heads        []string
}

func baseChanged() error {
	return &githubstack.CommandError{Command: "gh pr merge 41 --squash", Cause: errors.New("synthetic exit"), Output: "GraphQL: Base branch was modified. Review and try the merge again. (mergePullRequest)"}
}

func (f *retryGitHub) Merge(ctx context.Context, number int, method githubstack.Method, admin bool, head string) error {
	f.calls++
	f.heads = append(f.heads, head)
	if f.calls <= f.refuse {
		if f.afterRefusal != nil {
			f.afterRefusal()
		}
		return baseChanged()
	}
	return f.fakeGitHub.Merge(ctx, number, method, admin, head)
}

func TestMergeRetriesOnlyTheBaseChangeAndKeepsItsHeadPinned(t *testing.T) {
	for _, refusals := range []int{1, 2, 3} {
		t.Run(string(rune('0'+refusals)), func(t *testing.T) {
			w := newWorld(t)
			plan := w.plan(t, Defaults())
			retry := &retryGitHub{fakeGitHub: w.github, refuse: refusals}
			w.service.GitHub = retry
			waits := 0
			w.service.pause = func(context.Context, time.Duration) error { waits++; return nil }
			err := w.service.mergeReady(context.Background(), plan, plan.Steps[0], "one-tip")
			if refusals < 3 && err != nil {
				t.Fatal(err)
			}
			if refusals == 3 && (err == nil || !strings.Contains(err.Error(), "rerun g2g land") || !githubstack.BaseModified(err)) {
				t.Fatalf("error = %v, want bounded retry advice and retained diagnostic", err)
			}
			want := min(refusals+1, 3)
			if retry.calls != want || waits != want-1 {
				t.Fatalf("calls = %d, waits = %d; want %d, %d", retry.calls, waits, want, want-1)
			}
			for _, head := range retry.heads {
				if head != "one-tip" {
					t.Fatalf("head = %q", head)
				}
			}
		})
	}
}

func TestMergeRetryRechecksReviewProtectionConflictAndIdentity(t *testing.T) {
	for name, change := range map[string]func(*world){
		"approval withdrawn": func(w *world) {
			state := w.github.states[41]
			state.Review = "CHANGES_REQUESTED"
			w.github.states[41] = state
		},
		"protection changed": func(w *world) {
			state := w.github.states[41]
			state.StateStatus = "BLOCKED"
			w.github.states[41] = state
		},
		"conflict": func(w *world) {
			state := w.github.states[41]
			state.Mergeable = "CONFLICTING"
			w.github.states[41] = state
		},
		"draft":              func(w *world) { state := w.github.states[41]; state.Draft = true; w.github.states[41] = state },
		"base changed":       func(w *world) { w.github.prs[0].Base = "synthetic-other" },
		"local head changed": func(w *world) { w.git.objects["synthetic-one"] = "synthetic-new-head" },
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			plan := w.plan(t, Defaults())
			retry := &retryGitHub{fakeGitHub: w.github, refuse: 1, afterRefusal: func() { change(w) }}
			w.service.GitHub = retry
			w.service.pause = func(context.Context, time.Duration) error { return nil }
			err := w.service.mergeReady(context.Background(), plan, plan.Steps[0], "one-tip")
			if err == nil || retry.calls != 1 {
				t.Fatalf("error = %v, attempts = %d; unsafe retry", err, retry.calls)
			}
		})
	}
}

func TestMergeDoesNotRetryOtherFailuresOrOutliveCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		w := newWorld(t)
		plan := w.plan(t, Defaults())
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if cancelled {
			retry := &retryGitHub{fakeGitHub: w.github, refuse: 1}
			w.service.GitHub = retry
			w.service.pause = func(ctx context.Context, _ time.Duration) error { cancel(); return ctx.Err() }
			err := w.service.mergeReady(ctx, plan, plan.Steps[0], "one-tip")
			if !errors.Is(err, context.Canceled) || retry.calls != 1 {
				t.Fatalf("error = %v, calls = %d", err, retry.calls)
			}
		} else {
			w.github.mergeErr = errors.New("synthetic ambiguous transport failure")
			err := w.service.mergeReady(ctx, plan, plan.Steps[0], "one-tip")
			if !errors.Is(err, w.github.mergeErr) || len(w.events.only("merge:")) != 1 {
				t.Fatalf("retried ambiguous error: %v %v", err, w.events.seen)
			}
		}
	}
}

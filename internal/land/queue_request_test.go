package land

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/githubstack"
)

type uncertainQueueClient struct {
	*fakeGitHub
	requested, diagnosticLive bool
	requestErr                error
	cancel                    context.CancelFunc
}

func (f *uncertainQueueClient) RequestQueue(ctx context.Context, number int, id, head string, autoMerge ...bool) error {
	if err := f.fakeGitHub.RequestQueue(ctx, number, id, head, autoMerge...); err != nil {
		return err
	}
	f.requested = true
	f.cancel()
	return f.requestErr
}

func (f *uncertainQueueClient) Mergeability(ctx context.Context, numbers []int, trunk ...string) (githubstack.Mergeability, error) {
	if !f.requested {
		return f.fakeGitHub.Mergeability(ctx, numbers, trunk...)
	}
	_, bounded := ctx.Deadline()
	f.diagnosticLive = ctx.Err() == nil && bounded
	return githubstack.Mergeability{}, errors.New("synthetic diagnostic read failed")
}

func TestQueueRequestWithFailedDiagnosisRemainsPending(t *testing.T) {
	for _, requestFailed := range []bool{false, true} {
		t.Run(map[bool]string{false: "request accepted", true: "request outcome unknown"}[requestFailed], func(t *testing.T) {
			w := queuedWorld(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client := &uncertainQueueClient{fakeGitHub: w.github, cancel: cancel}
			want := "merge requested"
			if requestFailed {
				client.requestErr = errors.New("synthetic request transport failure")
				want = "merge request outcome unknown"
			}
			w.service.GitHub = client
			stopped := pendingFor(t, w.service.Apply(ctx, w.plan(t, Defaults())))
			if stopped.Pending.State != want || !strings.Contains(stopped.Pending.Retry, "--branch synthetic-two") || !client.diagnosticLive {
				t.Fatalf("lost uncertain outcome or independent diagnostic budget: %+v, live=%t", stopped.Pending, client.diagnosticLive)
			}
			if len(w.events.only("queue:")) != 1 || len(w.events.only("merge:")) != 0 || len(w.events.only("sync:")) != 0 || len(w.events.only("delete-")) != 0 {
				t.Fatalf("uncertain request retried or cleaned up: %v", w.events.seen)
			}
		})
	}
}

func TestQueueAcceptedRequestStillChecksQueueEntryAndPolicy(t *testing.T) {
	for _, change := range []string{"entry head", "queue disabled"} {
		t.Run(change, func(t *testing.T) {
			w := queuedWorld(t)
			w.github.afterQueue = func(number int) {
				state := w.github.states[number]
				if change == "entry head" {
					state.QueueEntry = &githubstack.QueueEntry{HeadCommit: &struct {
						OID string `json:"oid"`
					}{OID: "synthetic-other-head"}}
				} else {
					state.QueueEnabled = false
				}
				w.github.states[number] = state
			}
			err := w.service.Apply(context.Background(), w.plan(t, Defaults()))
			var stopped *Stopped
			if !errors.As(err, &stopped) || len(stopped.Landed) != 0 {
				t.Fatalf("unsafe accepted request treated as merged: %v", err)
			}
			if len(w.events.only("queue:")) != 1 || len(w.events.only("merge:")) != 0 || len(w.events.only("sync:")) != 0 || len(w.events.only("delete-")) != 0 {
				t.Fatalf("continued after queue changed: %v", w.events.seen)
			}
		})
	}
}

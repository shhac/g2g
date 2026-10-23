package land

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// Replace the policy rather than editing the pointer the preview still holds.
func changeQueuePolicy(w *world, field string) {
	changed := *w.github.queue
	switch field {
	case "identity":
		changed.ID = "MQ_synthetic_other"
	case "method":
		changed.Configuration = &struct {
			MergeMethod string `json:"mergeMethod"`
		}{MergeMethod: "SQUASH"}
	case "auto-merge":
		changed.AutoMergeAllowed = !changed.AutoMergeAllowed
	}
	w.github.queue = &changed
}

func TestQueuePolicyChangesStopBeforeEnrollmentOrDuringWait(t *testing.T) {
	for _, phase := range []string{"before enrollment", "while waiting"} {
		for _, field := range []string{"identity", "method", "auto-merge"} {
			t.Run(phase+"/"+field, func(t *testing.T) {
				w := queuedWorld(t)
				options := Defaults()
				options.Wait = phase == "while waiting"
				plan := w.plan(t, options)
				if options.Wait {
					w.service.pause = func(context.Context, time.Duration) error {
						changeQueuePolicy(w, field)
						return nil
					}
				} else {
					// A push finishes under the old preview; the fresh policy read
					// immediately before enrollment must notice the replacement.
					w.pusher.planned = func() { changeQueuePolicy(w, field) }
				}
				err := w.service.Apply(context.Background(), plan)
				var stopped *Stopped
				if !errors.As(err, &stopped) || len(stopped.Landed) != 0 || !strings.Contains(err.Error(), "merge queue policy changed") {
					t.Fatalf("changed policy was not refused: %v", err)
				}
				requests := 0
				if options.Wait {
					requests = 1
				}
				if len(w.events.only("queue:")) != requests || len(w.events.only("merge:")) != 0 || len(w.events.only("sync:")) != 0 || len(w.events.only("delete-")) != 0 {
					t.Fatalf("continued past changed policy: %v", w.events.seen)
				}
			})
		}
	}
}

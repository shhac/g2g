package land

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/stack"
)

func queuedWorld(t *testing.T) *world {
	w := newWorld(t)
	w.github.queue = &githubstack.Queue{ID: "MQ_synthetic", AutoMergeAllowed: true}
	// A queue can use merge despite the CLI's default squash being disallowed.
	w.github.allowed = githubstack.Allowed{Merge: true}
	state := w.github.states[41]
	state.QueueEnabled = true
	w.github.states[41] = state
	return w
}
func pendingFor(t *testing.T, err error) *Stopped {
	t.Helper()
	var stopped *Stopped
	if !errors.As(err, &stopped) || stopped.Pending == nil || !stopped.PartWay() {
		t.Fatalf("error = %v, want typed pending stop", err)
	}
	if len(stopped.Landed) != 0 {
		t.Fatalf("pending request reported landed: %+v", stopped)
	}
	return stopped
}

func TestQueueDefaultHandsOffWithoutReplayingOrDeleting(t *testing.T) {
	w := queuedWorld(t)
	plan := w.plan(t, Defaults())
	if plan.Blocked() != "" {
		t.Fatal(plan.Blocked())
	}
	commands := plan.Commands()
	for _, command := range commands {
		if strings.Contains(command.Command, "prune") || strings.Contains(command.Command, "--delete") || strings.Contains(command.Command, "pull --apply") {
			t.Fatalf("unconfirmed cleanup in recipe: %+v", commands)
		}
	}
	stopped := pendingFor(t, w.service.Apply(context.Background(), plan))
	if !strings.Contains(stopped.Pending.Retry, "--branch synthetic-two") || !strings.Contains(stopped.Pending.Retry, "--scope stack") {
		t.Fatalf("unscoped retry: %s", stopped.Pending.Retry)
	}
	if len(w.events.only("queue:")) != 1 || len(w.events.only("merge:")) != 0 || len(w.events.only("sync:")) != 0 || len(w.events.only("delete-")) != 0 {
		t.Fatalf("events = %v", w.events.seen)
	}
}
func TestQueueExistingRequestIsObservedWithoutMutation(t *testing.T) {
	w := queuedWorld(t)
	state := w.github.states[41]
	state.AutoMerge = true
	w.github.states[41] = state
	pendingFor(t, w.service.Apply(context.Background(), w.plan(t, Defaults())))
	if len(w.events.seen) != 0 {
		t.Fatalf("observing request mutated: %v", w.events.seen)
	}
}
func TestQueueRefusesExistingRequestWithDifferentHeadOrBase(t *testing.T) {
	for _, change := range []string{"head", "base"} {
		t.Run(change, func(t *testing.T) {
			w := queuedWorld(t)
			state := w.github.states[41]
			state.AutoMerge = true
			if change == "head" {
				state.HeadOID = "other-head"
			} else {
				state.Base = "other-base"
			}
			w.github.states[41] = state
			if plan := w.plan(t, Defaults()); !strings.Contains(plan.Blocked(), "different head or base") {
				t.Fatalf("Blocked = %q", plan.Blocked())
			}
			if len(w.events.seen) != 0 {
				t.Fatal(w.events.seen)
			}
		})
	}
}
func TestQueueProtectsPendingChildOutsideSelectedTarget(t *testing.T) {
	w := queuedWorld(t)
	landingTheBottom(w)
	state := w.github.states[42]
	state.AutoMerge = true
	w.github.states[42] = state
	if plan := w.plan(t, Defaults()); !strings.Contains(plan.Blocked(), "synthetic-two (#42) has a pending") {
		t.Fatalf("Blocked = %q", plan.Blocked())
	}
}
func TestQueueFlagsCannotBypassOrChooseMethod(t *testing.T) {
	for _, name := range []string{"admin", "method"} {
		t.Run(name, func(t *testing.T) {
			w := queuedWorld(t)
			options := Defaults()
			if name == "admin" {
				options.Admin = true
			} else {
				options.MethodChosen = true
			}
			if plan := w.plan(t, options); !strings.Contains(plan.Blocked(), "cannot override") {
				t.Fatalf("Blocked=%q", plan.Blocked())
			}
		})
	}
}
func TestQueueWaitCanConfirmAndDescend(t *testing.T) {
	w := queuedWorld(t)
	options := Defaults()
	options.Wait = true
	w.github.afterQueue = func(number int) {
		if err := w.github.Merge(context.Background(), number, githubstack.MethodSquash, false, w.github.states[number].HeadOID); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.service.Apply(context.Background(), w.plan(t, options)); err != nil {
		t.Fatal(err)
	}
	if len(w.events.only("queue:")) != 2 || len(w.events.only("sync:")) != 2 {
		t.Fatalf("events=%v", w.events.seen)
	}
}
func TestQueueWaitTimeoutRemainsPending(t *testing.T) {
	w := queuedWorld(t)
	options := Defaults()
	options.Wait = true
	w.service.pause = func(context.Context, time.Duration) error { return context.DeadlineExceeded }
	pendingFor(t, w.service.Apply(context.Background(), w.plan(t, options)))
	if len(w.events.only("sync:")) != 0 {
		t.Fatal(w.events.seen)
	}
}
func TestQueueWaitRefusesRemovalAndHeadChanges(t *testing.T) {
	for _, change := range []string{"removed", "head", "base", "closed"} {
		t.Run(change, func(t *testing.T) {
			w := queuedWorld(t)
			options := Defaults()
			options.Wait = true
			w.service.pause = func(context.Context, time.Duration) error {
				state := w.github.states[41]
				switch change {
				case "removed":
					state.AutoMerge = false
				case "head":
					state.HeadOID = "changed"
				case "base":
					state.Base = "changed"
				case "closed":
					state.State = "CLOSED"
				}
				w.github.states[41] = state
				return nil
			}
			err := w.service.Apply(context.Background(), w.plan(t, options))
			if err == nil {
				t.Fatal("wait succeeded")
			}
			if len(w.events.only("sync:")) != 0 {
				t.Fatal(w.events.seen)
			}
			var stopped *Stopped
			if !errors.As(err, &stopped) || len(stopped.Landed) != 0 {
				t.Fatalf("false merged: %v", err)
			}
		})
	}
}
func TestQueueProtectsChildQueuedDuringWait(t *testing.T) {
	w := queuedWorld(t)
	options := Defaults()
	options.Wait = true
	w.service.pause = func(context.Context, time.Duration) error {
		state := w.github.states[42]
		state.AutoMerge = true
		w.github.states[42] = state
		return w.github.Merge(context.Background(), 41, githubstack.MethodSquash, false, "one-tip")
	}
	err := w.service.Apply(context.Background(), w.plan(t, options))
	var stopped *Stopped
	if !errors.As(err, &stopped) || len(stopped.Landed) != 1 || !strings.Contains(err.Error(), "pending merge request") {
		t.Fatalf("error=%v", err)
	}
	if len(w.events.only("sync:")) != 0 {
		t.Fatal(w.events.seen)
	}
}
func TestDirectMergeNotReportedUntilGitHubConfirms(t *testing.T) {
	w := newWorld(t)
	w.github.afterMerge = func(number int) {
		state := w.github.states[number]
		state.State = "OPEN"
		state.MergeCommit = ""
		w.github.states[number] = state
	}
	w.service.pause = func(context.Context, time.Duration) error { return context.DeadlineExceeded }
	var stopped *Stopped
	err := w.service.Apply(context.Background(), w.plan(t, Defaults()))
	if !errors.As(err, &stopped) || len(stopped.Landed) != 0 {
		t.Fatalf("error=%v landed=%+v", err, stopped)
	}
}
func TestObservedMergeStillReportedWhenAncestryPropagationFails(t *testing.T) {
	w := newWorld(t)
	w.git.ancestors = map[string][]string{}
	w.service.pause = func(context.Context, time.Duration) error { return context.DeadlineExceeded }
	var stopped *Stopped
	err := w.service.Apply(context.Background(), w.plan(t, Defaults()))
	if !errors.As(err, &stopped) || len(stopped.Landed) != 1 {
		t.Fatalf("error=%v landed=%+v", err, stopped)
	}
}
func TestQueueSurvivorProtectionFindsNewRootAfterTidying(t *testing.T) {
	w := queuedWorld(t)
	w.store.graph = w.store.graph.Untrack("synthetic-one")
	plan := Plan{Discovery: stack.Discovery{Snapshot: stack.Snapshot{Branches: []string{"synthetic-one", "synthetic-two"}}}}
	// A root which no longer exists cannot hide the next branch's subtree.
	state := w.github.states[42]
	state.AutoMerge = true
	w.github.states[42] = state
	plan.Queue = nil
	plan.PullRequests = w.github.prs
	found, err := w.service.readSurvivors(context.Background(), plan, w.store.graph)
	if err != nil {
		t.Fatal(err)
	}
	note, err := w.service.pendingSurvivors(context.Background(), plan, found, githubstack.Mergeability{}, true)
	if err != nil || !strings.Contains(note.Reason, "synthetic-two") {
		t.Fatalf("note=%+v err=%v", note, err)
	}
}

func TestQueueRefusesKnownRequiredFailuresBeforeRequest(t *testing.T) {
	for _, name := range []string{"failed", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			w := queuedWorld(t)
			state := w.github.states[41]
			state.StateStatus = "BLOCKED"
			state.Checks = githubstack.Checks{Complete: true}
			if name == "failed" {
				state.Checks.Failed = []string{"synthetic-build"}
			} else {
				state.Checks.Cancelled = []string{"synthetic-lint"}
			}
			w.github.states[41] = state
			plan := w.plan(t, Defaults())
			if !strings.Contains(plan.Blocked(), name+" required checks") {
				t.Fatalf("blocked=%q", plan.Blocked())
			}
			if len(w.events.only("queue:")) != 0 {
				t.Fatal(w.events.seen)
			}
		})
	}
}
func TestQueueReviewAdviceDoesNotOfferRefusedAdminFlag(t *testing.T) {
	w := queuedWorld(t)
	state := w.github.states[41]
	state.Review = "REVIEW_REQUIRED"
	w.github.states[41] = state
	plan := w.plan(t, Defaults())
	if !strings.Contains(plan.Blocked(), "not approved") || strings.Contains(plan.Blocked(), "--admin") {
		t.Fatal(plan.Blocked())
	}
}
func TestCheckDiagnosticsDistinguishUnknownAndPending(t *testing.T) {
	for _, test := range []struct {
		name   string
		checks githubstack.Checks
		want   string
	}{
		{"unknown", githubstack.Checks{}, "remaining requirement is unknown"},
		{"pending", githubstack.Checks{Complete: true, Pending: []string{"synthetic-build"}}, "waiting for required checks: synthetic-build"},
		{"truncated", githubstack.Checks{Complete: false, Pending: []string{"synthetic-build"}}, "remaining requirement is unknown"},
		{"failed", githubstack.Checks{Complete: true, Failed: []string{"synthetic-build"}}, "failed required checks: synthetic-build"},
		{"cancelled", githubstack.Checks{Complete: true, Cancelled: []string{"synthetic-build"}}, "cancelled required checks: synthetic-build"},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := newWorld(t)
			state := w.github.states[41]
			state.StateStatus = "BLOCKED"
			state.Checks = test.checks
			w.github.states[41] = state
			plan := w.plan(t, Defaults())
			if !strings.Contains(plan.Blocked(), test.want) {
				t.Fatal(plan.Blocked())
			}
		})
	}
}

type ambiguousQueueClient struct{ *fakeGitHub }

func (f ambiguousQueueClient) RequestQueue(ctx context.Context, number int, id, head string, autoMerge ...bool) error {
	if err := f.fakeGitHub.RequestQueue(ctx, number, id, head, autoMerge...); err != nil {
		return err
	}
	return errors.New("synthetic transport failure after acceptance")
}
func TestQueueObservesAmbiguousRequestBeforeConsideringRetry(t *testing.T) {
	w := queuedWorld(t)
	w.service.GitHub = ambiguousQueueClient{w.github}
	pendingFor(t, w.service.Apply(context.Background(), w.plan(t, Defaults())))
	if len(w.events.only("queue:")) != 1 || len(w.events.only("merge:")) != 0 {
		t.Fatal(w.events.seen)
	}
}
func TestQueueIncompleteOrOptionalChecksCanBeHandedOff(t *testing.T) {
	w := queuedWorld(t)
	state := w.github.states[41]
	state.StateStatus = "BLOCKED"
	state.Checks = githubstack.Checks{}
	w.github.states[41] = state
	plan := w.plan(t, Defaults())
	if plan.Blocked() != "" {
		t.Fatal(plan.Blocked())
	}
	pendingFor(t, w.service.Apply(context.Background(), plan))
}

func TestContentLandedDoesNotDeletePendingRequestBranch(t *testing.T) {
	w := queuedWorld(t)
	w.git.absorbed["synthetic-one"] = true
	state := w.github.states[41]
	state.AutoMerge = true
	w.github.states[41] = state
	plan := w.plan(t, Defaults())
	if !strings.Contains(plan.Blocked(), "pending merge request although its content is in the trunk") {
		t.Fatal(plan.Blocked())
	}
	if err := w.service.Apply(context.Background(), plan); err == nil {
		t.Fatal("applied blocked cleanup")
	}
	if len(w.events.seen) != 0 {
		t.Fatal(w.events.seen)
	}
}
func TestExistingPendingFailedChecksRemainTypedPending(t *testing.T) {
	for _, name := range []string{"failed", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			w := queuedWorld(t)
			state := w.github.states[41]
			state.AutoMerge = true
			if name == "failed" {
				state.Checks.Failed = []string{"synthetic-build"}
			} else {
				state.Checks.Cancelled = []string{"synthetic-lint"}
			}
			w.github.states[41] = state
			stopped := pendingFor(t, w.service.Apply(context.Background(), w.plan(t, Defaults())))
			if !strings.Contains(stopped.Pending.Error(), name+" required checks") {
				t.Fatal(stopped.Pending)
			}
			if len(w.events.seen) != 0 {
				t.Fatal(w.events.seen)
			}
		})
	}
}
func TestQueueWithoutAutoMergeRequiresPrerequisitesAndUsesAPIRecipe(t *testing.T) {
	w := queuedWorld(t)
	w.github.queue.AutoMergeAllowed = false
	plan := w.plan(t, Defaults())
	if plan.Blocked() != "" {
		t.Fatal(plan.Blocked())
	}
	commands := plan.Commands()
	if len(commands) == 0 || !strings.Contains(commands[len(commands)-1].Command, "enqueuePullRequest") || strings.Contains(commands[len(commands)-1].Command, "gh pr merge") {
		t.Fatal(commands)
	}
	pendingFor(t, w.service.Apply(context.Background(), plan))
	w = queuedWorld(t)
	w.github.queue.AutoMergeAllowed = false
	state := w.github.states[41]
	state.StateStatus = "BLOCKED"
	state.Checks = githubstack.Checks{Complete: true, Pending: []string{"synthetic-build"}}
	w.github.states[41] = state
	plan = w.plan(t, Defaults())
	if !strings.Contains(plan.Blocked(), "disables auto-merge") || !strings.Contains(plan.Blocked(), "waiting for required checks") {
		t.Fatal(plan.Blocked())
	}
}

func TestQueueRequestRefusesPRPolicyDisagreement(t *testing.T) {
	w := queuedWorld(t)
	plan := w.plan(t, Defaults())
	state := w.github.states[41]
	state.QueueEnabled = false
	w.github.states[41] = state
	err := w.service.Apply(context.Background(), plan)
	if err == nil || !strings.Contains(err.Error(), "does not report a required merge queue") {
		t.Fatalf("err=%v", err)
	}
	if len(w.events.only("queue:")) != 0 || len(w.events.only("merge:")) != 0 {
		t.Fatal(w.events.seen)
	}
}

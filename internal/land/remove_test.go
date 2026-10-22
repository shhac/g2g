package land

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/push"
)

// The remote branch is deleted only at the head that landed. A reviewer can
// push to it after the merge, and that commit is on the remote alone; an
// unleased delete took it with the branch.

// The preflight assessed the remote, then somebody pushed before planning read
// it again. The later read is what Step.RemoteTip records, and it was never
// assessed: the lease stays on the tip the preflight checked, so the new
// commit survives.
func TestALaterRemoteReadNeverReplacesThePreflightsCheckedTip(t *testing.T) {
	w := newWorld(t)
	w.git.absorbed["synthetic-one"] = true
	w.git.absorbed["synthetic-reviewer-revert"] = true
	w.git.objects["synthetic-one"] = "main-tip"
	w.github.prs[0].State = "MERGED"
	w.github.states[41] = githubstack.MergeState{Number: 41, HeadOID: "one-tip", Base: "synthetic-main", State: "MERGED", MergeCommit: "merge-41"}
	w.pusher.assessed = map[string]push.Standing{"synthetic-one": push.Dropping}
	w.pusher.planned = func() { w.git.tips["synthetic-one"] = "synthetic-reviewer-revert" }

	plan := w.plan(t, Defaults())
	if step := plan.Steps[0]; step.RemoteTip != "synthetic-reviewer-revert" || step.LandedHead != "one-tip" {
		t.Fatalf("step = %+v, want the later read recorded and the preflight's one-tip as its landed head", step)
	}
	if err := w.service.Apply(context.Background(), plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if got := w.git.tips["synthetic-one"]; got != "synthetic-reviewer-revert" {
		t.Errorf("remote synthetic-one = %q, want the commit pushed after the preflight kept", got)
	}
	if w.events.index("delete-remote:synthetic-one@one-tip") < 0 {
		t.Errorf("want the delete leased on one-tip, got %v", w.events.only("delete-remote:"))
	}
}

// replayingTwo makes the first sync replay synthetic-two onto new commits, as
// pull does after the merge below it, so the head that merges in its turn is
// not the one the plan saw.
func replayingTwo(w *world) {
	w.syncer.replayed = func() {
		if w.git.objects["synthetic-two"] != "two-tip" {
			return
		}
		w.git.objects["synthetic-two"] = "two-replayed"
		state := w.github.states[42]
		state.HeadOID = "two-replayed"
		w.github.states[42] = state
	}
}

func TestRemoteDeletionIsLeasedOnTheHeadEachCycleMerged(t *testing.T) {
	w := newWorld(t)
	replayingTwo(w)
	plan := w.plan(t, Defaults())
	if plan.Steps[1].HeadOID != "two-tip" {
		t.Fatalf("planned head of synthetic-two = %q, want two-tip", plan.Steps[1].HeadOID)
	}

	if err := w.service.Apply(context.Background(), plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	want := []string{"delete-remote:synthetic-one@one-tip", "delete-remote:synthetic-two@two-replayed"}
	if got := w.events.only("delete-remote:"); !slices.Equal(got, want) {
		t.Errorf("remote deletions = %v, want %v", got, want)
	}
	if len(w.git.tips) != 0 {
		t.Errorf("remote still holds %v, want both landed branches gone", w.git.tips)
	}
}

// A reviewer pushes to the branch after it merged, while the descent waits,
// replays and cleans up. Their commit stays; the cleanup says it could not
// delete, and the descent carries on as cleanup failures always have.
func TestARemoteBranchPushedAfterItsMergeSurvivesCleanup(t *testing.T) {
	w := newWorld(t)
	w.github.afterMerge = func(number int) {
		if number == 41 {
			w.git.tips["synthetic-one"] = "synthetic-reviewer-fix"
		}
	}
	plan := w.plan(t, Defaults())
	var warnings bytes.Buffer
	ctx := diagnostic.WithWarningWriter(context.Background(), &warnings)

	if err := w.service.Apply(ctx, plan); err != nil {
		t.Fatalf("Apply() error = %v, want cleanup to stay nonfatal", err)
	}
	if got := w.git.tips["synthetic-one"]; got != "synthetic-reviewer-fix" {
		t.Errorf("remote synthetic-one = %q, want the reviewer's commit kept", got)
	}
	if w.events.index("delete-remote:synthetic-one@one-tip") < 0 {
		t.Errorf("want the delete leased on one-tip, got %v", w.events.seen)
	}
	if got := w.events.only("merge:"); len(got) != 2 {
		t.Errorf("merges = %v, want the descent to go on to #42", got)
	}
	if !strings.Contains(warnings.String(), "could not delete synthetic-one on origin") {
		t.Errorf("warnings = %q, want the cleanup reported", warnings.String())
	}
}

func TestQueuedRemoteDeletionIsLeasedOnTheHeadTheQueueMerged(t *testing.T) {
	w := queuedWorld(t)
	replayingTwo(w)
	options := Defaults()
	options.Wait = true
	w.github.afterQueue = func(number int) {
		if err := w.github.Merge(context.Background(), number, githubstack.MethodMerge, false, w.github.states[number].HeadOID); err != nil {
			t.Fatal(err)
		}
		if number == 41 {
			w.git.tips["synthetic-one"] = "synthetic-reviewer-fix"
		}
	}

	if err := w.service.Apply(context.Background(), w.plan(t, options)); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	want := []string{"delete-remote:synthetic-one@one-tip", "delete-remote:synthetic-two@two-replayed"}
	if got := w.events.only("delete-remote:"); !slices.Equal(got, want) {
		t.Errorf("remote deletions = %v, want %v", got, want)
	}
	if got := w.git.tips["synthetic-one"]; got != "synthetic-reviewer-fix" {
		t.Errorf("remote synthetic-one = %q, want the reviewer's commit kept", got)
	}
	if _, kept := w.git.tips["synthetic-two"]; kept {
		t.Errorf("remote still holds synthetic-two at %s, want it deleted at the merged head", w.git.tips["synthetic-two"])
	}
}

// A branch somebody else merged is only cleaned up here, and the plan's read of
// the remote may already hold a commit pushed after that merge. The remote's
// tip is the lease only where push's preflight found it holds nothing of the
// remote's own -- as for the merged head the remote keeps once a replay here
// collapsed the branch onto the trunk, which push reads as commits dropped
// here. Otherwise the lease is the head here that landed. Whether the remote's
// tip merges into the trunk without change never decides: a reviewer's revert
// of the squashed work does.
func TestAnAlreadyLandedBranchIsDeletedOnlyAtAHeadThatLanded(t *testing.T) {
	for name, testCase := range map[string]struct {
		// local is the branch here, whose work Git finds in the trunk; remote
		// what the remote holds when the descent is planned, and standing what
		// push's preflight made of it; after is what the remote holds by the
		// time the cleanup runs, when something moves it.
		local, remote, after string
		standing             push.Standing
		lease                string
		kept                 string
	}{
		"remote at the landed head":                 {local: "one-tip", remote: "one-tip", standing: push.Current, lease: "one-tip"},
		"pushed to after the merge":                 {local: "one-tip", remote: "synthetic-reviewer-fix", standing: push.Behind, lease: "one-tip", kept: "synthetic-reviewer-fix"},
		"pushed to after the plan":                  {local: "one-tip", remote: "one-tip", standing: push.Current, after: "synthetic-reviewer-fix", lease: "one-tip", kept: "synthetic-reviewer-fix"},
		"recreated after the plan":                  {local: "one-tip", after: "synthetic-recreated", lease: "one-tip", kept: "synthetic-recreated"},
		"already deleted by the remote":             {local: "one-tip", lease: "one-tip"},
		"collapsed here onto the trunk":             {local: "main-tip", remote: "one-tip", standing: push.Dropping, lease: "one-tip"},
		"collapsed here, pushed to after the plan":  {local: "main-tip", remote: "one-tip", standing: push.Dropping, after: "synthetic-reviewer-fix", lease: "one-tip", kept: "synthetic-reviewer-fix"},
		"collapsed here, unassessed":                {local: "main-tip", remote: "one-tip", lease: "main-tip", kept: "one-tip"},
		"collapsed here, reverted after the merge":  {local: "main-tip", remote: "synthetic-reviewer-revert", standing: push.Diverged, lease: "main-tip", kept: "synthetic-reviewer-revert"},
		"collapsed here, pushed to after the merge": {local: "main-tip", remote: "synthetic-reviewer-fix", standing: push.Behind, lease: "main-tip", kept: "synthetic-reviewer-fix"},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			w.git.absorbed["synthetic-one"] = true
			// The whole-branch merge says a revert of squashed work is in the
			// trunk, and so does every remote tip here; it must not matter.
			w.git.absorbed[testCase.remote] = true
			if testCase.standing != push.Uncompared {
				w.pusher.assessed = map[string]push.Standing{"synthetic-one": testCase.standing}
			}
			w.git.objects["synthetic-one"] = testCase.local
			w.github.prs[0].State = "MERGED"
			w.github.states[41] = githubstack.MergeState{Number: 41, HeadOID: "one-tip", Base: "synthetic-main", State: "MERGED", MergeCommit: "merge-41"}
			delete(w.git.tips, "synthetic-one")
			if testCase.remote != "" {
				w.git.tips["synthetic-one"] = testCase.remote
			}
			plan := w.plan(t, Defaults())
			if !plan.Steps[0].Landed || plan.Steps[0].RemoteTip != testCase.remote {
				t.Fatalf("step = %+v, want synthetic-one landed with the remote read as %q", plan.Steps[0], testCase.remote)
			}
			if testCase.after != "" {
				w.git.tips["synthetic-one"] = testCase.after
			}

			if err := w.service.Apply(context.Background(), plan); err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			if w.events.index("delete-remote:synthetic-one@"+testCase.lease) < 0 {
				t.Errorf("want the delete leased on %s, got %v", testCase.lease, w.events.only("delete-remote:"))
			}
			if got := w.git.tips["synthetic-one"]; got != testCase.kept {
				t.Errorf("remote synthetic-one = %q, want %q", got, testCase.kept)
			}
		})
	}
}

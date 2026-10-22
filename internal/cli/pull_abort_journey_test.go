package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/cli"
)

// indecisive user, after a pull: the pull took a reviewer's commit onto your
// branch, the replay onto the trunk it advanced conflicted, and you abandon
// the replay. What the pull took stays taken -- it recorded that it now agrees
// with the remote -- so the abort puts back the replay and nothing else.
//
// Putting the branch back before the take dropped the reviewer's commit here
// while the sync point said this clone had it. Where the branch still sat on
// origin/synthetic-a, as here, the next pull happened to put the commit back,
// reading the abort as a reset to a stale tracking ref. Where it did not --
// the branch replayed since it was pushed, as in
// TestJourneySharedStackAConflictingPullCanBeAbandoned -- the commit read as
// one you dropped, and push offered to publish the drop.

// reviewerAdds commits name onto synthetic-a in the other clone and publishes
// it, returning the published tip.
func (w *world) reviewerAdds(name string) string {
	w.t.Helper()
	if w.git(w.Other, "branch", "--list", "synthetic-a") == "" {
		w.git(w.Other, "fetch", "-q", "origin")
		w.git(w.Other, "switch", "-q", "-c", "synthetic-a", "origin/synthetic-a")
	}
	w.commit(w.Other, "synthetic-a", name, "reviewed")
	w.git(w.Other, "push", "-q", "origin", "synthetic-a")
	return w.tip(w.Other, "synthetic-a")
}

// trunkCollidesWith publishes a trunk commit writing name, so replaying a
// branch that also wrote it conflicts.
func (w *world) trunkCollidesWith(name string) {
	w.t.Helper()
	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "switch", "-q", "main")
	w.git(w.Other, "merge", "-q", "--ff-only", "origin/main")
	w.commit(w.Other, "main", name, "the trunk's version")
	w.git(w.Other, "push", "-q", "origin", "main")
}

// recordedFork is where the graph records branch forking, checked against the
// ref that pins it: the two have to agree for the record to be usable.
func (w *world) recordedFork(branch string) string {
	w.t.Helper()
	var stored struct {
		Branches map[string]struct {
			ForkPoint string `json:"forkPoint"`
		} `json:"branches"`
	}
	if err := json.Unmarshal([]byte(w.readStore()), &stored); err != nil {
		w.t.Fatalf("decode graph store: %v", err)
	}
	fork := stored.Branches[branch].ForkPoint
	if pinned := w.tip(w.Local, "refs/g2g/forkpoints/"+branch); pinned != fork {
		w.t.Errorf("%s is recorded forking at %s and pinned at %s", branch, fork, pinned)
	}
	return fork
}

// pullStopsThenAbort runs a pull that must stop on its replay, and abandons
// the replay.
func pullStopsThenAbort(t *testing.T) {
	t.Helper()
	stdout, _, err := run(t, "pull", "--apply")
	if !cli.StoppedPartWayForTest(err) {
		t.Fatalf("pull error = %v, want it stopped on the conflict:\n%s", err, stdout)
	}
	mustRun(t, "restack", "--abort")
}

// assertTookAndAgrees checks the state an abandoned replay leaves after a pull
// took published: the branch is the published version, the sync point still
// agrees with it and with the remote, the fork is where it was recorded, and
// neither command reads the reviewer's commit as one you dropped.
func (w *world) assertTookAndAgrees(published, fork, reviewed string) {
	w.t.Helper()
	if got := w.tip(w.Local, "synthetic-a"); got != published {
		w.t.Errorf("abort left synthetic-a at %s, want the version the pull took, %s", got, published)
	}
	if got := w.tip(w.Remote, "synthetic-a"); got != published {
		w.t.Errorf("the remote's synthetic-a moved to %s", got)
	}
	if got := w.tip(w.Local, "refs/g2g/synced/origin/synthetic-a"); got != published {
		w.t.Errorf("the sync point is at %s, want %s", got, published)
	}
	if got := w.recordedFork("synthetic-a"); got != fork {
		w.t.Errorf("synthetic-a is recorded forking at %s, want %s", got, fork)
	}
	if !w.contains(w.Local, fork, "synthetic-a") {
		w.t.Error("the recorded fork is not in synthetic-a")
	}
	// The trunk the pull advanced stays advanced: the abort is the replay's.
	if local, remote := w.tip(w.Local, "main"), w.tip(w.Remote, "main"); local != remote {
		w.t.Errorf("main is at %s, want the advanced %s", local, remote)
	}
	w.assertHas(w.Local, "synthetic-a", reviewed)
	w.assertClean(w.Local)

	if push := mustRun(w.t, "push"); strings.Contains(push, "Drops") {
		w.t.Errorf("push reads the reviewer's commit as one dropped here:\n%s", push)
	}
	if pull, _, _ := run(w.t, "pull"); strings.Contains(pull, "you dropped") {
		w.t.Errorf("pull leaves the reviewer's commit for push to drop:\n%s", pull)
	}
	if status := mustRun(w.t, "status"); strings.Contains(status, "moved off parent") || strings.Contains(status, "fork point") {
		w.t.Errorf("the abort left the structure invalid:\n%s", status)
	}
}

func TestJourneyAbortingAPullReplayKeepsTheReviewersCommitItTook(t *testing.T) {
	w := newWorld(t)
	base := w.tip(w.Local, "main")
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "push", "--apply")

	published := w.reviewerAdds("review.txt")
	w.trunkCollidesWith("a.txt")
	pullStopsThenAbort(t)

	w.assertTookAndAgrees(published, base, "review.txt")
}

// A second take, after a first pull took the reviewer's first commit without
// moving origin/synthetic-a. The abort keeps both commits and the sync point
// that agrees with them, rather than leaving the branch between the two takes.
func TestJourneyAbortingASecondPullReplayKeepsTheReviewersCommitItTook(t *testing.T) {
	w := newWorld(t)
	base := w.tip(w.Local, "main")
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "push", "--apply")
	pushed := w.tip(w.Local, "synthetic-a")

	first := w.reviewerAdds("review.txt")
	mustRun(t, "pull", "--apply")
	if got := w.tip(w.Local, "synthetic-a"); got != first {
		t.Fatalf("the first pull left synthetic-a at %s, want %s", got, first)
	}
	if got := w.tip(w.Local, "refs/remotes/origin/synthetic-a"); got != pushed {
		t.Fatalf("origin/synthetic-a moved to %s; this journey needs it left where the push put it", got)
	}

	second := w.reviewerAdds("review-again.txt")
	w.trunkCollidesWith("a.txt")
	pullStopsThenAbort(t)

	w.assertTookAndAgrees(second, base, "review-again.txt")
	w.assertHas(w.Local, "synthetic-a", "review.txt")
}

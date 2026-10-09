package cli_test

import (
	"strings"
	"testing"
)

// Drops between two people on one stack. Alice publishes main < A < B; Bob
// adopts it, adds commits x and y to B and publishes; Alice pulls them. Both
// clones have now agreed on a B holding x, which is what lets each later tell
// x being dropped from x being new.
func newDropWorld(t *testing.T) sharedStack {
	t.Helper()
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	mustRun(t, "adopt", "--trunk", "main", "--apply")
	mustRun(t, "push", "--apply")

	s := sharedStack{w}
	s.asBob()
	w.git(w.Other, "fetch", "-q", "origin")
	for _, branch := range []string{"synthetic-a", "synthetic-b"} {
		w.git(w.Other, "branch", "-q", branch, "origin/"+branch)
	}
	w.git(w.Other, "switch", "-q", "synthetic-b")
	mustRun(t, "adopt", "--scope", "path", "--trunk", "main", "--apply")
	w.commit(w.Other, "synthetic-b", "x.txt", "x")
	w.commit(w.Other, "synthetic-b", "y.txt", "y")
	mustRun(t, "push", "--apply")

	s.asAlice()
	mustRun(t, "pull", "--apply")
	s.assertHas(w.Local, "synthetic-b", "x.txt")
	return s
}

// dropX takes x out from under y on a clone's B, as git rebase -i would.
func (s sharedStack) dropX(dir string) string {
	s.t.Helper()
	x := s.tip(dir, "synthetic-b~1")
	s.git(dir, "switch", "-q", "synthetic-b")
	s.git(dir, "rebase", "-q", "--onto", x+"~1", x, "synthetic-b")
	return x
}

// bobDropsX drops x on Bob's B and publishes the drop with g2g push, which
// publishes it because Bob's sync point shows he had x and dropped it.
func (s sharedStack) bobDropsX() string {
	s.t.Helper()
	x := s.dropX(s.Other)
	s.asBob()
	preview := mustRun(s.t, "push")
	if !strings.Contains(preview, "Drops 1 published commit") || !strings.Contains(preview, "synthetic-b "+x[:12]+" synthetic x.txt") {
		s.t.Errorf("Bob's push preview does not name the commit it drops:\n%s", preview)
	}
	mustRun(s.t, "push", "--apply")
	if s.contains(s.Remote, x, "synthetic-b") {
		s.t.Error("Bob's push left the dropped commit on the remote")
	}
	s.asAlice()
	return x
}

// Alice has not pulled since Bob dropped x. Her push would put it back, so it
// refuses, naming the pull that drops it and the one that keeps it.
func TestJourneyPushRefusesToPutBackACommitTheRemoteDropped(t *testing.T) {
	s := newDropWorld(t)
	x := s.bobDropsX()
	mustRun(t, "pull") // the preview fetches, so the remote's tip is here to compare

	stdout, _, err := run(t, "push", "--apply")
	if err == nil {
		t.Fatal("Alice's push put back the commit Bob dropped")
	}
	for _, want := range []string{"would put it back", "g2g pull --keep " + x[:12]} {
		if !strings.Contains(stdout+err.Error(), want) {
			t.Errorf("the refusal does not say %q:\n%s\n%v", want, stdout, err)
		}
	}
}

// dropped upstream: Bob dropped x and published that. Alice's pull drops it
// too, naming it, and her next push does not put it back.
func TestJourneyACommitDroppedUpstreamIsDroppedOnPull(t *testing.T) {
	s := newDropWorld(t)
	x := s.bobDropsX()

	preview := mustRun(t, "pull")
	if !strings.Contains(preview, "synthetic-b "+x[:12]+" synthetic x.txt") {
		t.Errorf("the preview does not name the commit it drops:\n%s", preview)
	}
	mustRun(t, "pull", "--apply")
	s.assertClean(s.Local)
	if s.contains(s.Local, x, "synthetic-b") {
		t.Error("Alice's synthetic-b still has the commit Bob dropped")
	}
	if push := mustRun(t, "push"); strings.Contains(push, "to publish") && strings.Contains(push, "synthetic-b") {
		t.Errorf("Alice's push would publish synthetic-b again:\n%s", push)
	}
	// Named again by status, for whoever did not read the pull.
	if status := mustRun(t, "status"); !strings.Contains(status, "Dropped recently: synthetic-b "+x[:12]+" (g2g pull, just now)") {
		t.Errorf("status does not name the commit the pull dropped:\n%s", status)
	}
	// Recoverable: the sync point's history still reaches it.
	if !strings.Contains(s.git(s.Local, "reflog", "show", "--format=%gs", "refs/g2g/synced/origin/synthetic-b"), "dropped="+x) {
		t.Error("the sync point does not record what the pull dropped")
	}
}

// --keep keeps a commit the remote dropped, as Alice's own: the next push
// publishes it.
func TestJourneyPullKeepKeepsACommitDroppedUpstream(t *testing.T) {
	s := newDropWorld(t)
	x := s.bobDropsX()

	if stdout, _, err := run(t, "pull", "--keep", x[:12], "--apply"); err != nil {
		t.Fatalf("pull --keep: %v\n%s", err, stdout)
	}
	if !s.contains(s.Local, x, "synthetic-b") {
		t.Fatal("--keep dropped the commit it was asked to keep")
	}
	mustRun(t, "push", "--apply")
	s.assertHas(s.Remote, "synthetic-b", "x.txt")

	// A commit that is not a drop of this pull is refused by name.
	if _, _, err := run(t, "pull", "--keep", s.tip(s.Local, "main"), "--apply"); err == nil || !strings.Contains(err.Error(), "--keep") {
		t.Errorf("pull --keep of something it would not drop: error = %v", err)
	}
}

// dropped here: Alice drops x herself. The remote still has it, and her pull
// must not fast-forward it back -- publishing the drop is push's business.
func TestJourneyPullLeavesACommitDroppedHere(t *testing.T) {
	s := newDropWorld(t)
	x := s.dropX(s.Local)
	before := s.tip(s.Local, "synthetic-b")

	preview := mustRun(t, "pull")
	if !strings.Contains(preview, "Leaves 1 commit you dropped") || !strings.Contains(preview, x[:12]) {
		t.Errorf("the preview does not say the drop is left for push:\n%s", preview)
	}
	mustRun(t, "pull", "--apply")
	if after := s.tip(s.Local, "synthetic-b"); after != before {
		t.Errorf("pull moved synthetic-b from %s to %s, putting the dropped commit back", before, after)
	}
	s.assertClean(s.Local)

	// And push publishes it, because it is hers to publish.
	mustRun(t, "push", "--apply")
	if s.contains(s.Remote, x, "synthetic-b") {
		t.Error("Alice's push left the commit she dropped on the remote")
	}
	s.assertHas(s.Remote, "synthetic-b", "y.txt")
}

// pull leaves the remote-tracking ref where it was, so after Alice pulled
// Bob's commits it still names B without them. Resetting B to it leaves out
// exactly what the pull brought, which looks like a deliberate drop and is not
// safe to publish as one: pull puts them back, and says so.
func TestJourneyAResetToAStaleTrackingRefIsPutBack(t *testing.T) {
	s := newDropWorld(t)
	s.git(s.Local, "switch", "-q", "synthetic-b")
	s.git(s.Local, "reset", "-q", "--hard", "origin/synthetic-b")

	preview := mustRun(t, "pull")
	if !strings.Contains(preview, "Puts back 2 commits") {
		t.Errorf("the preview does not say what it puts back:\n%s", preview)
	}
	mustRun(t, "pull", "--apply")
	s.assertHas(s.Local, "synthetic-b", "x.txt")
	s.assertHas(s.Local, "synthetic-b", "y.txt")
}

// Your own commits on top of a branch the remote dropped a commit from: taking
// the published version would lose them, and keeping yours would keep the
// drop, so pull refuses and names each way through.
func TestJourneyPullRefusesYourCommitsOverACommitDroppedUpstream(t *testing.T) {
	s := newDropWorld(t)
	x := s.bobDropsX()
	s.commit(s.Local, "synthetic-b", "mine.txt", "mine")
	before := s.tip(s.Local, "synthetic-b")

	stdout, _, err := run(t, "pull", "--apply")
	if err == nil {
		t.Fatal("pull chose between your commit and their drop")
	}
	for _, want := range []string{x[:12], "--keep " + x[:12], "--take published"} {
		if !strings.Contains(stdout+err.Error(), want) {
			t.Errorf("the refusal does not say %q:\n%s\n%v", want, stdout, err)
		}
	}
	if s.tip(s.Local, "synthetic-b") != before {
		t.Error("a refused pull moved synthetic-b")
	}
}

// moved across a boundary: Bob moves A back one commit, so the commit at its
// tip becomes B's, and publishes A. Alice's pull takes it off her A and keeps
// it in her B, where Bob put it.
func TestJourneyACommitMovedIntoTheChildStaysInTheChild(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.commit(w.Local, "synthetic-a", "moving.txt", "moving")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	mustRun(t, "adopt", "--trunk", "main", "--apply")
	mustRun(t, "push", "--apply")
	moving := w.tip(w.Local, "synthetic-a")

	s := sharedStack{w}
	s.asBob()
	w.git(w.Other, "fetch", "-q", "origin")
	for _, branch := range []string{"synthetic-a", "synthetic-b"} {
		w.git(w.Other, "branch", "-q", branch, "origin/"+branch)
	}
	w.git(w.Other, "switch", "-q", "synthetic-b")
	mustRun(t, "adopt", "--scope", "path", "--trunk", "main", "--apply")
	w.git(w.Other, "branch", "-f", "synthetic-a", "synthetic-a~1")
	mustRun(t, "restack", "--branch", "synthetic-b", "--absorb", "--apply")
	push := mustRun(t, "push")
	if !strings.Contains(push, "Moves 1 published commit") || !strings.Contains(push, "synthetic-a → synthetic-b") {
		t.Errorf("Bob's push preview does not say the commit moved:\n%s", push)
	}
	mustRun(t, "push", "--apply")

	s.asAlice()
	preview := mustRun(t, "pull")
	if !strings.Contains(preview, "synthetic-a → synthetic-b") {
		t.Errorf("the preview does not say the commit moved:\n%s", preview)
	}
	mustRun(t, "pull", "--apply")
	s.assertClean(w.Local)
	if w.contains(w.Local, moving, "synthetic-a") {
		t.Error("synthetic-a still has the commit that moved out of it")
	}
	if !w.contains(w.Local, moving, "synthetic-b") {
		t.Error("the moved commit left synthetic-b too")
	}
	// It stays put on the next restack: the record says where B begins now.
	if restack := mustRun(t, "restack"); strings.Contains(restack, "dropped") {
		t.Errorf("a restack after the pull would drop the moved commit:\n%s", restack)
	}
}

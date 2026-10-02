package cli_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// Publishing, then publishing again after more work. The second push is the
// one that matters: the preview has to say what it would send, and the refs
// have to actually arrive.
func TestJourneyPublishAStackAndThenAddToIt(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")

	mustRun(t, "push", "--apply")
	if local, remote := w.tip(w.Local, "synthetic-a"), w.tip(w.Remote, "synthetic-a"); local != remote {
		t.Fatalf("the branch did not reach the remote: local %s, remote %s", local, remote)
	}

	stdout, _, err := run(t, "push")
	if err != nil {
		t.Fatalf("push preview: %v\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "up to date") {
		t.Errorf("preview does not say the remote already has it:\n%s", stdout)
	}

	w.commit(w.Local, "synthetic-a", "more.txt", "more")
	stdout, _, err = run(t, "push")
	if err != nil {
		t.Fatalf("push preview: %v\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "1 commit to publish") {
		t.Errorf("preview does not say what it would send:\n%s", stdout)
	}
	mustRun(t, "push", "--apply")
	if local, remote := w.tip(w.Local, "synthetic-a"), w.tip(w.Remote, "synthetic-a"); local != remote {
		t.Errorf("the second push did not arrive: local %s, remote %s", local, remote)
	}
	w.assertClean(w.Local)
}

// Publishing a stack for the first time. submit pushes the refs itself, so the
// half that matters here is that they actually arrive — which no test checked,
// because submit had only ever pushed to a PATH fake.
func TestJourneySubmitPublishesTheRefsItCreatesPullRequestsFor(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a", "--apply")
	recorder := w.fakeGitHub(noPullRequests)

	specDir := t.TempDir()
	mustRun(t, "submit", "--write-spec", specDir)
	spec := filepath.Join(specDir, "submission.json")
	fillSpecTitles(t, spec)
	mustRun(t, "submit", "--spec", spec, "--apply")

	// The refs are real, so this is checkable against the real remote.
	for _, branch := range []string{"synthetic-a", "synthetic-b"} {
		if local, remote := w.tip(w.Local, branch), w.tip(w.Remote, branch); local != remote {
			t.Errorf("%s did not reach the remote: local %s, remote %s", branch, local, remote)
		}
	}
	// One pull request per branch.
	if got := strings.Count(strings.Join(recorder.Calls(), "\n"), "pr create"); got != 2 {
		t.Errorf("created %d pull requests, want one per branch", got)
	}
	w.assertClean(w.Local)
}

// The world moves between preview and apply. Revalidation exists precisely for
// this, and it had never been tested against a remote that actually moved.
func TestJourneyTheRemoteMovesBetweenPreviewAndApply(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "push", "--apply")

	mustRun(t, "push")

	// Between the two invocations, somebody publishes.
	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "switch", "-q", "-c", "synthetic-a", "origin/synthetic-a")
	w.commit(w.Other, "synthetic-a", "theirs.txt", "theirs")
	w.git(w.Other, "push", "-q", "origin", "synthetic-a")
	theirs := w.tip(w.Other, "synthetic-a")

	w.commit(w.Local, "synthetic-a", "yours.txt", "yours")
	if _, _, err := run(t, "push", "--apply"); err == nil {
		t.Fatal("push applied a plan the world had moved under")
	}
	if now := w.tip(w.Remote, "synthetic-a"); now != theirs {
		t.Errorf("the remote branch changed from %s to %s", theirs, now)
	}
}

// Atomicity: push claims every selected ref advances together or none does.
// One branch's lease failing has to leave the other exactly where it was.
func TestJourneyOneRejectedBranchStopsTheWholePush(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a", "--apply")
	mustRun(t, "push", "--apply")

	// Only the lower branch moves under us.
	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "switch", "-q", "-c", "synthetic-a", "origin/synthetic-a")
	w.commit(w.Other, "synthetic-a", "theirs.txt", "theirs")
	w.git(w.Other, "push", "-q", "origin", "synthetic-a")

	// And we add work to the upper one, which on its own would push cleanly.
	w.commit(w.Local, "synthetic-b", "more.txt", "more")
	untouched := w.tip(w.Remote, "synthetic-b")

	if _, _, err := run(t, "push", "--apply"); err == nil {
		t.Fatal("push proceeded with one branch the remote had moved")
	}
	if now := w.tip(w.Remote, "synthetic-b"); now != untouched {
		t.Errorf("synthetic-b advanced to %s despite the push being refused, want %s", now, untouched)
	}
}

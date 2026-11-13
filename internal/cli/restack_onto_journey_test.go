package cli_test

import (
	"strings"
	"testing"
)

// Replaying onto the remote's copy of a trunk, because the local one is
// checked out with work in progress, is an ordinary request. It recorded
// origin/main as the parent, and the next status read "parent missing" after a
// replay that had done what was asked. The local branch is what gets recorded,
// forking where the remote's copy is.
func TestJourneyRestackOntoTheRemotesCopyOfTheTrunk(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--parent", "main", "--apply")
	w.commit(w.Other, "main", "upstream.txt", "upstream")
	w.git(w.Other, "push", "-q", "origin", "main")
	w.git(w.Local, "fetch", "-q", "origin")
	upstream := w.tip(w.Local, "origin/main")

	preview := mustRun(t, "restack", "--onto", "origin/main")
	if !strings.Contains(preview, "onto origin/main, recorded under main") {
		t.Errorf("the preview does not say where the commits land and what is recorded:\n%s", preview)
	}
	mustRun(t, "restack", "--onto", "origin/main", "--apply")
	w.assertClean(w.Local)

	if !w.contains(w.Local, upstream, "synthetic-a") {
		t.Error("synthetic-a was not replayed onto origin/main")
	}
	if got := w.readStructure()["synthetic-a"]; got != "main" {
		t.Errorf("synthetic-a is recorded under %q, want main", got)
	}
	if got := w.tip(w.Local, "refs/g2g/forkpoints/synthetic-a"); got != upstream {
		t.Errorf("synthetic-a forks at %s, want origin/main's %s", got, upstream)
	}
	status := mustRun(t, "status")
	if strings.Contains(status, "parent missing") || strings.Contains(status, "needs restack") {
		t.Errorf("status reads the replay as broken:\n%s", status)
	}
	if again := mustRun(t, "restack"); !strings.Contains(again, "Nothing to replay") {
		t.Errorf("a second restack would replay again:\n%s", again)
	}

	if _, _, err := run(t, "restack", "--onto", "synthetic-nowhere"); err == nil || !strings.Contains(err.Error(), "names no local branch") {
		t.Errorf("an --onto naming no branch = %v, want a refusal", err)
	}
}

// A pull that leaves the trunk where it is fetches into g2g's own refs, and
// the remote-tracking ref stays behind. The note that the trunk is behind
// counts from the newer of the two, and the --onto it suggests resolved only
// the older: it replayed onto the stale trunk, and the next restack found
// nothing to do. Both answer from the same known tip now.
func TestJourneyRestackOntoTheRemotesCopyAsLastFetchedByG2G(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--parent", "main", "--apply")
	w.commit(w.Other, "main", "upstream.txt", "upstream")
	w.git(w.Other, "push", "-q", "origin", "main")
	w.git(w.Local, "fetch", "-q", "--refmap=", "--no-write-fetch-head", "origin", "+refs/heads/main:refs/g2g/remotes/origin/main")
	fetched := w.tip(w.Local, "refs/g2g/remotes/origin/main")
	if fetched == w.tip(w.Local, "origin/main") {
		t.Fatal("the remote-tracking ref moved, so this shows nothing")
	}

	mustRun(t, "restack", "--onto", "origin/main", "--apply")
	w.assertClean(w.Local)
	if !w.contains(w.Local, fetched, "synthetic-a") {
		t.Error("synthetic-a was replayed onto the stale remote-tracking ref rather than the fetched trunk")
	}
	if got := w.tip(w.Local, "refs/g2g/forkpoints/synthetic-a"); got != fetched {
		t.Errorf("synthetic-a forks at %s, want the fetched trunk %s", got, fetched)
	}
}

// --onto may name any remote's copy. The fork point was moved up to the
// default remote's alone, so one replayed onto another remote forked at the
// local trunk and read the remote's commits as its own.
func TestJourneyRestackOntoAnotherRemotesCopy(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--parent", "main", "--apply")
	w.git(w.Local, "remote", "add", "synthetic-upstream", w.Remote)
	w.commit(w.Other, "main", "upstream.txt", "upstream")
	w.git(w.Other, "push", "-q", "origin", "main")
	w.git(w.Local, "fetch", "-q", "synthetic-upstream")
	upstream := w.tip(w.Local, "synthetic-upstream/main")

	mustRun(t, "restack", "--onto", "synthetic-upstream/main", "--apply")
	if got := w.tip(w.Local, "refs/g2g/forkpoints/synthetic-a"); got != upstream {
		t.Errorf("synthetic-a forks at %s, want synthetic-upstream/main's %s", got, upstream)
	}
	if got := w.readStructure()["synthetic-a"]; got != "main" {
		t.Errorf("synthetic-a is recorded under %q, want main", got)
	}
}

// A replay onto a remote's copy that stops on a conflict is resumed onto the
// same commit, and recorded the same way, as one that did not stop.
func TestJourneyRestackOntoTheRemotesCopyResumesThere(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "shared.txt")
	mustRun(t, "track", "--parent", "main", "--apply")
	w.commit(w.Other, "main", "shared.txt", "the remote's version")
	w.git(w.Other, "push", "-q", "origin", "main")
	w.git(w.Local, "fetch", "-q", "origin")
	upstream := w.tip(w.Local, "origin/main")

	if _, _, err := run(t, "restack", "--onto", "origin/main", "--apply"); err == nil {
		t.Fatal("the replay did not stop on its conflict")
	}
	w.resolveRestack(w.Local)
	w.assertClean(w.Local)
	if !w.contains(w.Local, upstream, "synthetic-a") {
		t.Error("the resumed replay did not land on origin/main")
	}
	if got := w.readStructure()["synthetic-a"]; got != "main" {
		t.Errorf("synthetic-a is recorded under %q, want main", got)
	}
	if got := w.tip(w.Local, "refs/g2g/forkpoints/synthetic-a"); got != upstream {
		t.Errorf("synthetic-a forks at %s, want origin/main's %s", got, upstream)
	}
}

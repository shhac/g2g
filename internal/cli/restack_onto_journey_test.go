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

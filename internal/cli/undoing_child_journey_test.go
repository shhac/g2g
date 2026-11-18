package cli_test

import (
	"strings"
	"testing"
)

// undoingChild is a stack whose child's only commit takes back a file its
// parent added. Measured from where the stack left the trunk, the child
// changes less than its parent does and nothing the parent does not, so once
// the parent is in the trunk a merge of the child into it changes nothing at
// all -- which is also what a branch that has landed looks like.
func undoingChild(t *testing.T) *world {
	t.Helper()
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.commit(w.Local, "synthetic-a", "extra.txt", "synthetic extra")
	w.git(w.Local, "switch", "-q", "-c", "synthetic-b", "synthetic-a")
	w.git(w.Local, "rm", "-q", "extra.txt")
	w.git(w.Local, "commit", "-qm", "synthetic removal")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a", "--apply")
	w.git(w.Local, "push", "-q", "origin", "synthetic-a", "synthetic-b")
	return w
}

// assertKeepsRemoval fails unless the child still carries exactly its one
// commit, and that commit still removes the file.
func (w *world) assertKeepsRemoval(t *testing.T, dir, base, branch string) {
	t.Helper()
	own := strings.Fields(w.git(dir, "rev-list", base+".."+branch))
	if len(own) != 1 {
		t.Fatalf("%s has %d commits of its own on %s, want its one removal", branch, len(own), base)
	}
	if changed := w.git(dir, "diff", "--name-status", base, branch); changed != "D\textra.txt" {
		t.Fatalf("%s changes %q against %s, want the removal of extra.txt", branch, changed, base)
	}
}

// Landing the parent replays the child, and the child's commit goes with it.
// It was read as already in the trunk, moved onto the trunk with nothing on
// it, and published that way -- which emptied its pull request, and GitHub
// closed it.
func TestJourneyLandKeepsAChildThatUndoesPartOfItsParent(t *testing.T) {
	w := undoingChild(t)
	landingGitHub(t, w.Remote)

	mustRun(t, "land", "--branch", "synthetic-a", "--apply")

	w.git(w.Local, "fetch", "-q", "origin")
	w.assertKeepsRemoval(t, w.Local, "origin/main", "synthetic-b")
	w.assertKeepsRemoval(t, w.Local, "origin/main", "origin/synthetic-b")
	w.assertClean(w.Local)
}

// The same child after the parent was squash-merged by somebody else: pull
// replays it rather than collapsing it, status does not call it landed, and
// prune leaves it alone.
func TestJourneyPullKeepsAChildThatUndoesPartOfItsParent(t *testing.T) {
	w := undoingChild(t)
	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "merge", "-q", "--squash", "origin/synthetic-a")
	w.git(w.Other, "commit", "-qm", "synthetic squash")
	w.git(w.Other, "push", "-q", "origin", "main")

	if status := mustRun(t, "status", "--branch", "synthetic-b"); strings.Contains(status, "synthetic-b  landed") {
		t.Fatalf("status before the pull calls the child landed:\n%s", status)
	}
	mustRun(t, "pull", "--branch", "synthetic-b", "--apply")
	w.assertKeepsRemoval(t, w.Local, "main", "synthetic-b")
	w.assertClean(w.Local)

	status := mustRun(t, "status", "--branch", "synthetic-b")
	if strings.Contains(status, "Already in the trunk: synthetic-a and synthetic-b") || strings.Contains(status, "synthetic-b  landed") {
		t.Fatalf("status calls the child landed:\n%s", status)
	}
	mustRun(t, "prune", "--branch", "synthetic-b", "--delete-branches", "--apply")
	w.assertKeepsRemoval(t, w.Local, "main", "synthetic-b")
	if structure := w.readStructure(); structure["synthetic-b"] != "main" {
		t.Fatalf("recorded structure = %v, want the child on main", structure)
	}
}

// Deleted from the remote with its parent, the child is still work nobody
// has: push offers it rather than calling it landed.
func TestJourneyPushOffersAChildThatUndoesPartOfItsLandedParent(t *testing.T) {
	w := undoingChild(t)
	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "merge", "-q", "--squash", "origin/synthetic-a")
	w.git(w.Other, "commit", "-qm", "synthetic squash")
	w.git(w.Other, "push", "-q", "origin", "main", ":synthetic-a", ":synthetic-b")
	mustRun(t, "pull", "--branch", "synthetic-b", "--prune", "--apply")

	mustRun(t, "push", "--branch", "synthetic-b", "--apply")
	w.git(w.Local, "fetch", "-q", "origin")
	w.assertKeepsRemoval(t, w.Local, "origin/main", "origin/synthetic-b")
}

// Before anything replays the child, with the squash already in the local
// trunk, is where the whole branch looks most like one that landed: nothing
// has yet put its commit on top of the trunk. Every command that asks reads
// it as work still to do. prune would otherwise have forgotten the branch and
// deleted it, and push would have declined to publish it.
func TestJourneyAChildThatUndoesPartOfItsLandedParentIsNotCalledLanded(t *testing.T) {
	w := undoingChild(t)
	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "merge", "-q", "--squash", "origin/synthetic-a")
	w.git(w.Other, "commit", "-qm", "synthetic squash")
	w.git(w.Other, "push", "-q", "origin", "main", ":synthetic-b")
	mustRun(t, "pull", "--trunk-only", "--apply")
	w.git(w.Local, "switch", "-q", "main")
	tip := w.tip(w.Local, "synthetic-b")

	status := mustRun(t, "status", "--branch", "synthetic-b")
	if !strings.Contains(status, "Already in the trunk: synthetic-a ·") {
		t.Errorf("status does not call the parent alone landed:\n%s", status)
	}
	prune, _, _ := run(t, "prune", "--branch", "synthetic-b", "--delete-branches", "--apply")
	if strings.Contains(prune, "synthetic-b  landed") {
		t.Errorf("prune calls the child landed:\n%s", prune)
	}
	if w.tip(w.Local, "synthetic-b") != tip {
		t.Fatal("prune moved or deleted the child")
	}
	push := mustRun(t, "push", "--branch", "synthetic-b")
	if !strings.Contains(push, "synthetic-b  new branch on the remote") {
		t.Errorf("push does not offer the child:\n%s", push)
	}
}

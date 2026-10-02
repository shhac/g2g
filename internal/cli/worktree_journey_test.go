package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A detached HEAD has no branch for a rewrite to move underneath it, so the
// reconciliation that follows a rewrite has nothing to reconcile.
func TestJourneyWorkingFromADetachedHead(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	w.commit(w.Local, "main", "moved.txt", "moved")

	w.git(w.Local, "checkout", "-q", "--detach", "main")
	mustRun(t, "restack", "--branch", "synthetic-a", "--apply")

	w.assertClean(w.Local)
	if !w.contains(w.Local, "main", "synthetic-a") {
		t.Error("synthetic-a was not replayed while HEAD was detached")
	}
}

// A branch another worktree has checked out is refused, because a rewrite moves
// a ref without checking anything out and would strand that worktree.
func TestJourneyABranchIsOpenInASecondWorktree(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a", "--apply")

	// Git refuses to check a branch out twice, so step off it first: the point
	// is a branch open elsewhere, not one open in both.
	w.git(w.Local, "switch", "-q", "main")
	elsewhere := filepath.Join(t.TempDir(), "second")
	w.git(w.Local, "worktree", "add", "-q", elsewhere, "synthetic-b")
	w.commit(w.Local, "main", "moved.txt", "moved")
	w.git(w.Local, "switch", "-q", "synthetic-a")
	before := w.tip(w.Local, "synthetic-b")

	stdout, _, err := run(t, "restack", "--scope", "stack", "--apply")
	if err == nil {
		t.Fatal("a rewrite moved a branch another worktree had checked out")
	}
	if !strings.Contains(stdout+err.Error(), "another worktree") {
		t.Errorf("the refusal does not name the cause:\n%s\n%v", stdout, err)
	}
	if after := w.tip(w.Local, "synthetic-b"); after != before {
		t.Errorf("synthetic-b moved from %s to %s", before, after)
	}
	w.assertClean(elsewhere)
}

// The trunk is checked out in another worktree — the ordinary layout of a
// checkout with several — and somebody lands on it. sync advances the trunk
// itself rather than through the rewrite, so the rewrite's own check never saw
// it and the trunk moved underneath that worktree. It is refused now, and
// nothing moves.
func TestJourneySyncRefusesToAdvanceATrunkOpenInAnotherWorktree(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	elsewhere := filepath.Join(t.TempDir(), "second")
	w.git(w.Local, "worktree", "add", "-q", elsewhere, "main")

	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "switch", "-q", "main")
	w.git(w.Other, "pull", "-q", "origin", "main")
	w.commit(w.Other, "main", "landed.txt", "landed")
	w.git(w.Other, "push", "-q", "origin", "main")
	before := w.tip(w.Local, "main")

	stdout, _, err := run(t, "pull", "--apply")
	if err == nil {
		t.Fatalf("sync advanced a trunk another worktree had checked out:\n%s", stdout)
	}
	if !strings.Contains(stdout+err.Error(), "another worktree") {
		t.Errorf("the refusal does not name the cause:\n%s\n%v", stdout, err)
	}
	if after := w.tip(w.Local, "main"); after != before {
		t.Errorf("main moved from %s to %s", before, after)
	}
	w.assertClean(elsewhere)
}

// You stand on the trunk with an edit of your own, and what landed upstream
// touches the same file. Advancing the trunk would overwrite the edit, so it is
// refused — and refused before the trunk moves. It used to move first, leaving
// the trunk advanced under a tree that still described the old one, reported
// as not applied.
func TestJourneySyncOnADirtyTrunkMovesNothing(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	w.git(w.Local, "switch", "-q", "main")

	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "switch", "-q", "main")
	w.git(w.Other, "pull", "-q", "origin", "main")
	w.commit(w.Other, "main", "shared.txt", "upstream")
	w.git(w.Other, "push", "-q", "origin", "main")
	if err := os.WriteFile(filepath.Join(w.Local, "shared.txt"), []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := w.tip(w.Local, "main")

	if _, _, err := run(t, "pull", "--apply"); err == nil {
		t.Fatal("sync overwrote a local edit")
	}
	if after := w.tip(w.Local, "main"); after != before {
		t.Errorf("main moved from %s to %s although the checkout could not follow", before, after)
	}
	if status := w.git(w.Local, "status", "--porcelain"); status != "?? shared.txt" {
		t.Errorf("status = %q, want only the edit that was already there", status)
	}
}

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

// Asked to refuse, a branch another worktree has checked out is refused,
// because a rewrite moves a ref without checking anything out and would strand
// that worktree.
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

	stdout, _, err := run(t, "restack", "--scope", "stack", "--worktrees", "refuse", "--apply")
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
// it and the trunk moved underneath that worktree. Asked to refuse, it does,
// and nothing moves.
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

	stdout, _, err := run(t, "pull", "--worktrees", "refuse", "--apply")
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

// A second worktree on a branch of the stack, with nothing uncommitted: the
// restack moves the branch anyway and brings that worktree's files with it.
func TestJourneyACleanSecondWorktreeIsBroughtAlong(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a", "--apply")
	w.git(w.Local, "switch", "-q", "main")
	elsewhere := filepath.Join(t.TempDir(), "second")
	w.git(w.Local, "worktree", "add", "-q", elsewhere, "synthetic-b")
	w.commit(w.Local, "main", "moved.txt", "moved")
	w.git(w.Local, "switch", "-q", "synthetic-a")

	if preview := mustRun(t, "restack", "--scope", "stack"); !strings.Contains(preview, "Moves synthetic-b in ") || !strings.Contains(preview, filepath.Base(elsewhere)) {
		t.Errorf("the preview does not say the second worktree comes along:\n%s", preview)
	}
	mustRun(t, "restack", "--scope", "stack", "--apply")

	if !w.contains(w.Local, "main", "synthetic-b") {
		t.Error("synthetic-b was not replayed onto the moved trunk")
	}
	w.assertClean(w.Local)
	w.assertClean(elsewhere)
	if _, err := os.Stat(filepath.Join(elsewhere, "moved.txt")); err != nil {
		t.Errorf("the second worktree's files were not brought to its branch: %v", err)
	}
}

// The same with work in progress there: the branch stays, the rest moves, and
// the change is exactly where it was.
func TestJourneyASecondWorktreeWithChangesIsLeftAlone(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a", "--apply")
	w.git(w.Local, "switch", "-q", "main")
	elsewhere := filepath.Join(t.TempDir(), "second")
	w.git(w.Local, "worktree", "add", "-q", elsewhere, "synthetic-b")
	if err := os.WriteFile(filepath.Join(elsewhere, "b.txt"), []byte("work in progress\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.commit(w.Local, "main", "moved.txt", "moved")
	w.git(w.Local, "switch", "-q", "synthetic-a")
	before := w.tip(w.Local, "synthetic-b")

	stdout := mustRun(t, "restack", "--scope", "stack", "--apply")
	if !strings.Contains(stdout, "Leaves synthetic-b where it is") {
		t.Errorf("the report does not name the branch it left:\n%s", stdout)
	}
	if !w.contains(w.Local, "main", "synthetic-a") {
		t.Error("synthetic-a was not replayed")
	}
	if after := w.tip(w.Local, "synthetic-b"); after != before {
		t.Errorf("synthetic-b moved from %s to %s under somebody's work", before, after)
	}
	w.assertClean(w.Local)
	if status := w.git(elsewhere, "status", "--porcelain"); status != "M b.txt" {
		t.Errorf("status there = %q, want only the change that was already there", status)
	}
}

// The ordinary layout: the main checkout sits clean on the trunk, the work is
// in a linked worktree, and the trunk has moved upstream. pull advances the
// trunk there too, files and all, and replays the stack onto it.
func TestJourneyPullFollowsATrunkOpenInAnotherCleanWorktree(t *testing.T) {
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

	mustRun(t, "pull", "--apply")

	if w.tip(w.Local, "main") != w.tip(w.Remote, "main") {
		t.Error("main was not advanced to what the remote holds")
	}
	if !w.contains(w.Local, "main", "synthetic-a") {
		t.Error("synthetic-a was not replayed onto the advanced trunk")
	}
	w.assertClean(w.Local)
	w.assertClean(elsewhere)
	if _, err := os.Stat(filepath.Join(elsewhere, "landed.txt")); err != nil {
		t.Errorf("the trunk's worktree was not brought along: %v", err)
	}
}

// With work in progress on the trunk there, the trunk stays where it is and
// nothing is taken from the remote; the stack is restacked only against the
// trunk as it is here, which it already sits on.
func TestJourneyPullLeavesATrunkWhoseWorktreeHasChanges(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	elsewhere := filepath.Join(t.TempDir(), "second")
	w.git(w.Local, "worktree", "add", "-q", elsewhere, "main")
	if err := os.WriteFile(filepath.Join(elsewhere, "notes.txt"), []byte("in progress\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "switch", "-q", "main")
	w.git(w.Other, "pull", "-q", "origin", "main")
	w.commit(w.Other, "main", "landed.txt", "landed")
	w.git(w.Other, "push", "-q", "origin", "main")
	before, branch := w.tip(w.Local, "main"), w.tip(w.Local, "synthetic-a")

	stdout := mustRun(t, "pull", "--apply")
	if !strings.Contains(stdout, "Leaves main where it is") {
		t.Errorf("the report does not say the trunk was left:\n%s", stdout)
	}
	if strings.Contains(stdout, "already up to date") {
		t.Errorf("a pull that left the trunk behind reported being up to date:\n%s", stdout)
	}
	if w.tip(w.Local, "main") != before || w.tip(w.Local, "synthetic-a") != branch {
		t.Error("something moved although the trunk was left")
	}
	w.assertClean(w.Local)
	if status := w.git(elsewhere, "status", "--porcelain"); status != "?? notes.txt" {
		t.Errorf("status there = %q, want only the work that was already there", status)
	}
}

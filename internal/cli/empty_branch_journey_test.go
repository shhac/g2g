package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJourneyPullConflictKeepsAnEmptyTopBranchOnItsParent(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-parent", "base.txt")
	mustRun(t, "track", "--parent", "main", "--apply")
	w.branchOff("synthetic-parent", "synthetic-child", "child.txt")
	mustRun(t, "track", "--parent", "synthetic-parent", "--apply")
	w.git(w.Local, "switch", "-qc", "synthetic-empty")
	mustRun(t, "track", "--parent", "synthetic-child", "--apply")
	w.commit(w.Other, "main", "base.txt", "synthetic upstream")
	w.git(w.Other, "push", "-q", "origin", "main")

	preview := mustRun(t, "pull")
	if !strings.Contains(preview, "Moves synthetic-empty onto synthetic-child") {
		t.Fatalf("empty branch destination is wrong: %s", preview)
	}
	out, _, err := run(t, "pull", "--apply")
	if err == nil || !strings.Contains(out, "stopped on a conflict") {
		t.Fatalf("pull = %v, %s; want conflict", err, out)
	}
	if err := os.WriteFile(filepath.Join(w.Local, "base.txt"), []byte("synthetic resolved\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.git(w.Local, "add", "base.txt")
	mustRun(t, "restack", "--continue")
	if w.tip(w.Local, "synthetic-empty") != w.tip(w.Local, "synthetic-child") {
		t.Fatal("empty branch was left at its parent's old tip")
	}
	w.assertClean(w.Local)
	if w.git(w.Local, "branch", "--show-current") != "synthetic-empty" {
		t.Fatal("recovery did not return to the empty branch")
	}
	if out := mustRun(t, "status"); strings.Contains(statusLine(t, out, "synthetic-empty"), "needs restack") {
		t.Fatalf("recovered empty branch still needs restacking: %s", out)
	}
}

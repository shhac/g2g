package cli_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// A restack stopped on a conflict in another worktree is that worktree's
// business. It refused every mutation in every worktree, so an agent pushing a
// stack with nothing in common with it waited for somebody else's conflict.
// What it still refuses is work on the branches it is moving, and anything
// built on them.
func TestJourneyARestackStoppedElsewhereLeavesOtherStacksFree(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-busy", "shared.txt")
	mustRun(t, "track", "--parent", "main", "--apply")
	w.branchOff("main", "synthetic-free", "free.txt")
	mustRun(t, "track", "--parent", "main", "--apply")
	w.commit(w.Local, "main", "shared.txt", "the trunk's own version")

	agent := filepath.Join(filepath.Dir(w.Local), "agent")
	w.git(w.Local, "worktree", "add", "-q", agent, "synthetic-busy")
	t.Chdir(agent)
	if stdout, stderr, err := run(t, "restack", "--apply"); err == nil {
		t.Fatalf("the agent's restack did not stop on its conflict:\n%s%s", stdout, stderr)
	}
	t.Chdir(w.Local)
	w.git(w.Local, "switch", "-q", "synthetic-free")

	mustRun(t, "push", "--apply")
	if w.tip(w.Remote, "synthetic-free") != w.tip(w.Local, "synthetic-free") {
		t.Error("the unrelated stack was not published")
	}
	mustRun(t, "create", "synthetic-above-free", "--apply")
	w.assertClean(w.Local)

	w.git(w.Local, "switch", "-qc", "synthetic-loose", "synthetic-free")
	w.commit(w.Local, "synthetic-loose", "loose.txt", "loose")
	mustRun(t, "track", "--branch", "synthetic-loose", "--parent", "synthetic-free", "--apply")
	if got := w.readStructure()["synthetic-loose"]; got != "synthetic-free" {
		t.Errorf("synthetic-loose is recorded under %q, want synthetic-free", got)
	}

	for _, args := range [][]string{
		{"push", "--branch", "synthetic-busy", "--apply"},
		{"create", "synthetic-above-busy", "--parent", "synthetic-busy", "--apply"},
		{"track", "--branch", "synthetic-loose", "--parent", "synthetic-busy", "--apply"},
	} {
		_, stderr, err := run(t, args...)
		if err == nil || !strings.Contains(stderr+err.Error(), "already in progress") {
			t.Errorf("g2g %s went ahead while the restack moves its branch: %v", strings.Join(args, " "), err)
		}
	}
}

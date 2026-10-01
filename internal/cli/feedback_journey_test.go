package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJourneyTrunkOnlyPullLeavesAnUnrelatedStackAndItsChangesAlone(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a", "--apply")
	a, b := w.tip(w.Local, "synthetic-a"), w.tip(w.Local, "synthetic-b")
	graphPath := filepath.Join(w.Local, ".git", "g2g", "graph.json")
	before, err := os.ReadFile(graphPath)
	if err != nil {
		t.Fatal(err)
	}
	w.commit(w.Other, "main", "colleague.txt", "synthetic upstream")
	w.git(w.Other, "push", "-q", "origin", "main")
	dirty := filepath.Join(w.Local, "b.txt")
	if err := os.WriteFile(dirty, []byte("synthetic uncommitted work\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	status := w.git(w.Local, "status", "--porcelain")
	mustRun(t, "pull", "--trunk-only", "--apply")
	if w.tip(w.Local, "main") != w.tip(w.Remote, "main") {
		t.Fatal("trunk was not advanced")
	}
	if w.tip(w.Local, "synthetic-a") != a || w.tip(w.Local, "synthetic-b") != b {
		t.Fatal("trunk-only pull rewrote stack branches")
	}
	if w.git(w.Local, "status", "--porcelain") != status {
		t.Fatal("trunk-only pull changed uncommitted work")
	}
	after, err := os.ReadFile(graphPath)
	if err != nil || string(after) != string(before) {
		t.Fatalf("trunk-only pull changed the graph: %v", err)
	}
	if w.git(w.Local, "branch", "--show-current") != "synthetic-b" {
		t.Fatal("trunk-only pull moved checkout")
	}
}

func TestJourneyTrunkOnlyPullWorksBeforeRecordingAStack(t *testing.T) {
	w := newWorld(t)
	w.commit(w.Other, "main", "colleague.txt", "synthetic upstream")
	w.git(w.Other, "push", "-q", "origin", "main")
	mustRun(t, "pull", "--branch", "main", "--trunk-only", "--apply")
	w.assertClean(w.Local)
	if w.tip(w.Local, "main") != w.tip(w.Remote, "main") {
		t.Fatal("unrecorded default trunk was not advanced")
	}
}

func TestJourneyLandALeafLeavesATrunkHeldElsewhereUntouched(t *testing.T) {
	for _, keepLocal := range []bool{false, true} {
		t.Run(map[bool]string{false: "delete-local", true: "keep-local"}[keepLocal], func(t *testing.T) {
			w := newWorld(t)
			w.branchOff("main", "synthetic-a", "a.txt")
			w.commit(w.Local, "synthetic-a", "second.txt", "synthetic second change")
			mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
			w.git(w.Local, "push", "-q", "origin", "synthetic-a")
			held := filepath.Join(t.TempDir(), "synthetic-held")
			w.git(w.Local, "worktree", "add", "-q", held, "main")
			old := w.tip(w.Local, "main")
			landingGitHubStack(t, w.Remote, "synthetic-a")
			args := []string{"land", "--branch", "synthetic-a", "--no-comment"}
			if keepLocal {
				args = append(args, "--no-delete-local")
			}
			out := mustRun(t, args...)
			if !strings.Contains(out, "will be left at its current tip") {
				t.Fatalf("preview omitted held trunk: %s", out)
			}
			if w.tip(w.Remote, "main") != old {
				t.Fatal("preview merged")
			}
			mustRun(t, append(args, "--apply")...)
			w.assertClean(w.Local)
			w.assertClean(held)
			if w.tip(w.Local, "main") != old || w.tip(held, "HEAD") != old {
				t.Fatal("land moved the held trunk")
			}
			if w.tip(w.Remote, "main") == old {
				t.Fatal("merge did not reach the remote")
			}
			w.assertHas(w.Remote, "main", "a.txt")
			w.assertHas(w.Remote, "main", "second.txt")
			current := w.git(w.Local, "branch", "--show-current")
			if !keepLocal && current != "" {
				t.Fatalf("checkout = %q, want detached", current)
			}
			if keepLocal && current != "synthetic-a" {
				t.Fatalf("checkout = %q, want retained branch", current)
			}
			if !keepLocal && w.tip(w.Local, "HEAD") != w.tip(w.Remote, "main") {
				t.Fatal("checkout was not settled at the merge")
			}
			graphBytes, err := os.ReadFile(filepath.Join(w.Local, ".git", "g2g", "graph.json"))
			if err != nil || strings.Contains(string(graphBytes), "synthetic-a") {
				t.Fatalf("landed branch remains recorded: %v %s", err, graphBytes)
			}
			if remoteBranches := w.git(w.Local, "ls-remote", "--heads", "origin", "synthetic-a"); remoteBranches != "" {
				t.Fatalf("land left remote branch: %s", remoteBranches)
			}
			localBranches := w.git(w.Local, "branch", "--list", "synthetic-a")
			if (localBranches != "") != keepLocal {
				t.Fatalf("local branch = %q, keep = %v", localBranches, keepLocal)
			}
		})
	}
}

func TestJourneyLandRetriesABaseChangeBeforePerformingTheRealMerge(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	w.git(w.Local, "push", "-q", "origin", "synthetic-a")
	state := landingGitHubStack(t, w.Remote, "synthetic-a")
	if err := os.WriteFile(filepath.Join(state, "base-modified-41"), []byte("synthetic refusal"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "land", "--branch", "synthetic-a", "--no-comment", "--apply")
	w.assertClean(w.Local)
	w.assertHas(w.Remote, "main", "a.txt")
	attempts, err := os.ReadFile(filepath.Join(state, "merge-attempts"))
	if err != nil || len(strings.Fields(string(attempts))) != 2 {
		t.Fatalf("merge attempts = %q, %v; want 2", attempts, err)
	}
}

func TestJourneyTrunkOnlyPullRefusesATrunkHeldElsewhere(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	held := filepath.Join(t.TempDir(), "synthetic-held")
	w.git(w.Local, "worktree", "add", "-q", held, "main")
	old := w.tip(w.Local, "main")
	w.commit(w.Other, "main", "colleague.txt", "synthetic upstream")
	w.git(w.Other, "push", "-q", "origin", "main")
	out, _, err := run(t, "pull", "--trunk-only", "--apply")
	if err == nil || !strings.Contains(out, "checked out in another worktree") {
		t.Fatalf("pull error = %v, output = %s", err, out)
	}
	if w.tip(w.Local, "main") != old || w.tip(held, "HEAD") != old {
		t.Fatal("trunk-only pull moved the held trunk")
	}
	w.assertClean(w.Local)
	w.assertClean(held)
}

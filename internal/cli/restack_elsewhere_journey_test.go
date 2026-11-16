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

	// pull cannot say what it touches before it has advanced the trunk the
	// agent's restack replays onto, so it still waits for every restack.
	trunk := w.tip(w.Local, "main")
	if _, stderr, err := run(t, "pull", "--apply"); err == nil || !strings.Contains(stderr+err.Error(), "unfinished in checkout") {
		t.Errorf("pull went ahead beside another worktree's restack: %v", err)
	}
	if w.tip(w.Local, "main") != trunk {
		t.Error("the refused pull moved the trunk")
	}
	// doctor reports the agent's restack as the agent's, with its way past.
	if stdout, _, _ := run(t, "doctor"); !strings.Contains(stdout, `for branch "synthetic-busy" is unfinished in checkout`) || !strings.Contains(stdout, "wait for it") {
		t.Errorf("doctor does not report the agent's restack as the agent's:\n%s", stdout)
	}
	w.assertClean(w.Local)

	for _, args := range [][]string{
		{"push", "--branch", "synthetic-busy", "--apply"},
		{"create", "synthetic-above-busy", "--parent", "synthetic-busy", "--apply"},
		{"track", "--branch", "synthetic-loose", "--parent", "synthetic-busy", "--apply"},
	} {
		_, stderr, err := run(t, args...)
		if err == nil || !strings.Contains(stderr+err.Error(), "unfinished in checkout") {
			t.Errorf("g2g %s went ahead while the restack moves its branch: %v", strings.Join(args, " "), err)
		}
	}
}

// Two sessions restacking two stacks at once is the ordinary case once
// agents each have a worktree. One stopped on its conflict used to refuse the
// other's restack outright, naming a --continue and an --abort that belonged
// to somebody else, so the only way through was to wait for them.
func TestJourneyARestackStoppedElsewhereLeavesAnotherStacksRestackFree(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-busy", "shared.txt")
	mustRun(t, "track", "--parent", "main", "--apply")
	w.branchOff("main", "synthetic-free", "free.txt")
	mustRun(t, "track", "--parent", "main", "--apply")
	w.branchOff("synthetic-free", "synthetic-free-top", "free-top.txt")
	mustRun(t, "track", "--parent", "synthetic-free", "--apply")
	w.commit(w.Local, "main", "shared.txt", "the trunk's own version")
	w.commit(w.Local, "synthetic-free", "free.txt", "a reviewer's fix below the top")

	agent := filepath.Join(filepath.Dir(w.Local), "agent")
	w.git(w.Local, "worktree", "add", "-q", agent, "synthetic-busy")
	t.Chdir(agent)
	if stdout, stderr, err := run(t, "restack", "--apply"); err == nil {
		t.Fatalf("the agent's restack did not stop on its conflict:\n%s%s", stdout, stderr)
	}

	t.Chdir(w.Local)
	w.git(w.Local, "switch", "-q", "synthetic-free")
	stdout, stderr, err := run(t, "restack", "--apply")
	if err != nil {
		t.Fatalf("restacking an unrelated stack was refused: %v\n%s%s", err, stdout, stderr)
	}
	if !w.contains(w.Local, "synthetic-free", "synthetic-free-top") {
		t.Error("synthetic-free-top was not replayed onto synthetic-free")
	}
	w.assertClean(w.Local)

	// The agent's restack is still theirs to finish, and still refuses a
	// restack of what it is moving from here.
	if _, stderr, err := run(t, "restack", "--branch", "synthetic-busy", "--apply"); err == nil || !strings.Contains(stderr+err.Error(), "unfinished in checkout") {
		t.Errorf("restacking the branch the agent's restack is moving went ahead: %v", err)
	}
	t.Chdir(agent)
	if _, _, err := run(t, "restack", "--abort"); err != nil {
		t.Fatalf("the agent could not abort its own restack: %v", err)
	}
	w.assertClean(agent)
}

// GitHub-only writes depend on the selected structure, so a restack moving
// unrelated branches in another worktree must not prevent their repair.
func TestJourneyGitHubRepairsBesideUnrelatedRestack(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-busy", "shared.txt")
	mustRun(t, "track", "--parent", "main", "--apply")
	w.branchOff("main", "synthetic-free", "free.txt")
	mustRun(t, "track", "--parent", "main", "--apply")
	w.branchOff("synthetic-free", "synthetic-free-top", "top.txt")
	mustRun(t, "track", "--parent", "synthetic-free", "--apply")
	mustRun(t, "push", "--branch", "synthetic-busy", "--apply")
	mustRun(t, "push", "--branch", "synthetic-free", "--apply")
	state := landingGitHubPulls(t, w.Remote, [2]string{"synthetic-busy", "main"}, [2]string{"synthetic-free", "main"}, [2]string{"synthetic-free-top", "main"})
	w.commit(w.Local, "main", "shared.txt", "synthetic trunk conflict")
	agent := filepath.Join(filepath.Dir(w.Local), "agent")
	w.git(w.Local, "worktree", "add", "-q", agent, "synthetic-busy")
	t.Chdir(agent)
	if _, _, err := run(t, "restack", "--apply"); err == nil {
		t.Fatal("restack did not stop on conflict")
	}
	t.Chdir(w.Local)
	w.git(w.Local, "switch", "-q", "synthetic-free-top")
	before := w.tip(w.Remote, "synthetic-free-top")
	for _, command := range []string{"retarget", "comment"} {
		stdout, stderr, err := run(t, "github", command, "--branch", "synthetic-free-top", "--apply")
		if err != nil {
			t.Errorf("unrelated %s refused: %v\n%s%s", command, err, stdout, stderr)
		}
		if _, stderr, err := run(t, "github", command, "--branch", "synthetic-busy", "--apply"); err == nil || !strings.Contains(stderr+err.Error(), "unfinished in checkout") {
			t.Errorf("%s did not refuse the moving selection: %v", command, err)
		}
	}
	if got := readState(t, state, "pr-43.base"); got != "synthetic-free" {
		t.Errorf("retarget did not repair base: %s", got)
	}
	if got := readState(t, state, "comment-42"); got == "" {
		t.Error("comment did not keep the free stack")
	}
	if w.tip(w.Remote, "synthetic-free-top") != before {
		t.Error("GitHub repair moved a Git ref")
	}
	w.assertClean(w.Local)
	t.Chdir(agent)
	for _, command := range []string{"retarget", "comment"} {
		if _, _, err := run(t, "github", command, "--branch", "synthetic-free-top", "--apply"); err == nil {
			t.Errorf("%s ignored this worktree's unfinished restack", command)
		}
	}
	mustRun(t, "restack", "--abort")
	w.assertClean(agent)
}

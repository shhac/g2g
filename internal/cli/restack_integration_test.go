package cli_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// restackRepo builds a real stack and moves its trunk on, which is the shape
// every restack exists to repair. A rewrite is the one thing a PATH fake
// cannot prove — the fake answers whatever it is asked, and the question here
// is what Git actually produces — so this drives the real adapters end to end.
//
// Nothing leaves the machine: no remote is configured and every name is
// invented.
// syntheticEnv is the whole environment a throwaway repository needs: an
// identity to commit with, and no user configuration at all. Relying on the
// machine's own identity works on a developer's box and fails on a runner that
// has none, which is a difference between environments rather than in the code.
func syntheticEnv() []string {
	return append(os.Environ(),
		"GIT_AUTHOR_NAME=synthetic", "GIT_AUTHOR_EMAIL=synthetic@example.test",
		"GIT_COMMITTER_NAME=synthetic", "GIT_COMMITTER_EMAIL=synthetic@example.test",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
	)
}

func restackRepo(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	env := syntheticEnv()
	run := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir, command.Env = dir, env
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
		}
		return strings.TrimSpace(string(output))
	}
	write := func(contents string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "shared.txt"), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	run("init", "-q", "--initial-branch=synthetic-trunk")
	// Background maintenance writes into .git after a commit, which races the
	// temporary directory's cleanup. Disabling it in the repository covers
	// every invocation, including the ones the code under test makes.
	run("config", "gc.auto", "0")
	run("config", "maintenance.auto", "false")
	write("base\n")
	run("add", "-A")
	run("commit", "-qm", "base")
	run("checkout", "-qb", "synthetic-a")
	write("base\na\n")
	run("add", "-A")
	run("commit", "-qm", "a1")
	run("checkout", "-qb", "synthetic-b")
	write("base\na\nb\n")
	run("add", "-A")
	run("commit", "-qm", "b1")
	t.Chdir(dir)
	return dir
}

// advanceTrunk absorbs synthetic-a's work into the trunk as one squashed
// commit, exactly as a squash merge does, and optionally rewrites the same
// lines afterwards so the replay cannot apply cleanly.
//
// It runs after the stack is adopted, which is the real order: you record the
// structure while it is healthy, and the merge happens later.
func advanceTrunk(t *testing.T, overlap bool) {
	t.Helper()
	gitOutput(t, "checkout", "-q", "synthetic-trunk")
	gitOutput(t, "merge", "--squash", "-q", "synthetic-a")
	gitOutput(t, "commit", "-qm", "synthetic-a (#1)")
	if overlap {
		if err := os.WriteFile("shared.txt", []byte("base\nrewritten-upstream\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		gitOutput(t, "add", "-A")
		gitOutput(t, "commit", "-qm", "trunk rewrites the same lines")
	}
	gitOutput(t, "checkout", "-q", "synthetic-b")
}

func trackStack(t *testing.T) {
	t.Helper()
	for _, pair := range [][2]string{{"synthetic-a", "synthetic-trunk"}, {"synthetic-b", "synthetic-a"}} {
		if _, _, err := run(t, "track", "--branch", pair[0], "--parent", pair[1], "--apply"); err != nil {
			t.Fatalf("track %s: %v", pair[0], err)
		}
	}
}

// resolveAndContinue plays the person: keep both sides of every conflict, mark
// it resolved, and carry on until the operation reports it is done. A stack
// can stop more than once, and which commit it stops on is not something a
// test should be asserting.
func resolveAndContinue(t *testing.T, dir string) string {
	t.Helper()

	var stdout string
	for attempt := 0; attempt < 5; attempt++ {
		path := filepath.Join(dir, "shared.txt")
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(keepBothSides(string(contents))), 0o600); err != nil {
			t.Fatal(err)
		}
		gitOutput(t, "add", "shared.txt")
		var runErr error
		stdout, _, runErr = run(t, "restack", "--continue")
		if runErr == nil {
			return stdout
		}
		if _, err := os.Stat(filepath.Join(dir, ".git", "g2g", "restack.json")); os.IsNotExist(err) {
			t.Fatalf("restack --continue: %v\n%s", runErr, stdout)
		}
	}
	t.Fatalf("the restack never finished:\n%s", stdout)
	return ""
}

// keepBothSides removes conflict markers, keeping the content from each side.
func keepBothSides(contents string) string {
	kept := make([]string, 0)
	for _, line := range strings.Split(contents, "\n") {
		switch {
		case strings.HasPrefix(line, "<<<<<<<"), strings.HasPrefix(line, "======="), strings.HasPrefix(line, ">>>>>>>"):
			continue
		case line == "":
			continue
		default:
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n") + "\n"
}

func gitOutput(t *testing.T, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Env = syntheticEnv()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func isAncestor(t *testing.T, ancestor, descendant string) bool {
	t.Helper()
	command := exec.Command("git", "merge-base", "--is-ancestor", ancestor, descendant)
	command.Env = syntheticEnv()
	return command.Run() == nil
}

// The whole point of the clean engine: the stack is repaired and the user's
// checkout is never touched.
func TestRestackReplaysTheStackWithoutTouchingTheCheckout(t *testing.T) {
	restackRepo(t)
	trackStack(t)
	advanceTrunk(t, false)
	head := gitOutput(t, "branch", "--show-current")

	stdout, _, err := run(t, "restack", "--scope", "graph", "--apply")
	if err != nil {
		t.Fatalf("restack --apply: %v\n%s", err, stdout)
	}

	if !isAncestor(t, "synthetic-trunk", "synthetic-b") {
		t.Error("synthetic-b was not replayed onto the trunk")
	}
	if !isAncestor(t, "synthetic-a", "synthetic-b") {
		t.Error("synthetic-b is no longer built on synthetic-a")
	}
	if now := gitOutput(t, "branch", "--show-current"); now != head {
		t.Errorf("HEAD moved from %q to %q", head, now)
	}
	if status := gitOutput(t, "status", "--porcelain"); status != "" {
		t.Errorf("the working tree is dirty after a clean restack:\n%s", status)
	}
	if !strings.Contains(stdout, "without touching your working tree") {
		t.Errorf("output does not say the checkout was left alone:\n%s", stdout)
	}
}

// Restacking the bottom of a stack and leaving everything above it is the
// failure this guards: a branch whose parent moves has to move too.
func TestRestackCarriesDescendantsNotJustTheBottomBranch(t *testing.T) {
	restackRepo(t)
	trackStack(t)
	advanceTrunk(t, false)
	before := gitOutput(t, "rev-parse", "synthetic-b")

	if _, _, err := run(t, "restack", "--scope", "graph", "--apply"); err != nil {
		t.Fatal(err)
	}

	if gitOutput(t, "rev-parse", "synthetic-b") == before {
		t.Fatal("synthetic-b was left behind while its parent was rewritten")
	}
	if commits := gitOutput(t, "log", "--oneline", "synthetic-trunk..synthetic-b"); !strings.Contains(commits, "b1") {
		t.Errorf("synthetic-b lost its own commit:\n%s", commits)
	}
}

// A branch whose content is entirely upstream collapses onto its base, and a
// pull request for it would show nothing. That has to be said out loud.
func TestRestackReportsABranchItEmpties(t *testing.T) {
	restackRepo(t)
	trackStack(t)
	advanceTrunk(t, false)

	stdout, _, err := run(t, "restack", "--scope", "graph")
	if err != nil {
		t.Fatalf("restack: %v\n%s", err, stdout)
	}

	if !strings.Contains(stdout, "no commits of its own") {
		t.Errorf("output does not report the emptied branch:\n%s", stdout)
	}
	if !strings.Contains(stdout, "synthetic-a") {
		t.Errorf("output does not name the emptied branch:\n%s", stdout)
	}
}

func TestRestackPreviewChangesNothing(t *testing.T) {
	restackRepo(t)
	trackStack(t)
	advanceTrunk(t, false)
	before := gitOutput(t, "rev-parse", "synthetic-b")

	stdout, _, err := run(t, "restack", "--scope", "graph")
	if err != nil {
		t.Fatal(err)
	}

	if gitOutput(t, "rev-parse", "synthetic-b") != before {
		t.Error("the preview rewrote a branch")
	}
	if !strings.Contains(stdout, "No changes were made") {
		t.Errorf("output does not say so:\n%s", stdout)
	}
}

// Taking over the working tree is announced before it happens, which is only
// possible because the preview engine has no side effects.
func TestRestackWarnsBeforeItTakesOverTheWorkingTree(t *testing.T) {
	restackRepo(t)
	trackStack(t)
	advanceTrunk(t, true)

	stdout, _, err := run(t, "restack", "--scope", "graph")
	if err != nil {
		t.Fatalf("restack: %v\n%s", err, stdout)
	}

	if !strings.Contains(stdout, "will not apply cleanly") {
		t.Errorf("output does not predict the conflict:\n%s", stdout)
	}
	if !strings.Contains(stdout, "rebases in your working tree") {
		t.Errorf("output does not warn about the working tree:\n%s", stdout)
	}
}

// The full interrupted round trip: stop on a conflict, resolve it the way a
// person would, and continue.
func TestRestackStopsOnConflictThenContinues(t *testing.T) {
	dir := restackRepo(t)
	trackStack(t)
	advanceTrunk(t, true)

	stdout, _, _ := run(t, "restack", "--scope", "graph", "--apply")
	if !strings.Contains(stdout, "Stopped on a conflict") {
		t.Fatalf("apply did not stop on the conflict:\n%s", stdout)
	}
	journal := filepath.Join(dir, ".git", "g2g", "restack.json")
	if _, err := os.Stat(journal); err != nil {
		t.Fatalf("no journal to resume from: %v", err)
	}

	// Every other command must refuse while this is unfinished.
	if _, _, err := run(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-trunk", "--apply"); err == nil {
		t.Error("track was allowed during an interrupted restack")
	}

	stdout = resolveAndContinue(t, dir)
	if !strings.Contains(stdout, "Restack complete") {
		t.Errorf("output does not report completion:\n%s", stdout)
	}
	if _, err := os.Stat(journal); !os.IsNotExist(err) {
		t.Errorf("the journal outlived the operation (stat error = %v)", err)
	}
	if !isAncestor(t, "synthetic-trunk", "synthetic-b") {
		t.Error("synthetic-b is not on the trunk after continuing")
	}
}

// Abort restores every branch, including one an earlier step already moved.
func TestRestackAbortRestoresEveryBranch(t *testing.T) {
	dir := restackRepo(t)
	trackStack(t)
	advanceTrunk(t, true)
	original := map[string]string{
		"synthetic-a": gitOutput(t, "rev-parse", "synthetic-a"),
		"synthetic-b": gitOutput(t, "rev-parse", "synthetic-b"),
	}

	if stdout, _, _ := run(t, "restack", "--scope", "graph", "--apply"); !strings.Contains(stdout, "Stopped on a conflict") {
		t.Fatalf("apply did not stop:\n%s", stdout)
	}

	stdout, _, err := run(t, "restack", "--abort")
	if err != nil {
		t.Fatalf("restack --abort: %v\n%s", err, stdout)
	}

	for branch, was := range original {
		if now := gitOutput(t, "rev-parse", branch); now != was {
			t.Errorf("%s = %s after abort, want the original %s", branch, now, was)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "g2g", "restack.json")); !os.IsNotExist(err) {
		t.Error("abort left the journal behind")
	}
	if status := gitOutput(t, "status", "--porcelain"); status != "" {
		t.Errorf("abort left the working tree dirty:\n%s", status)
	}
}

// A branch someone rebased by hand has a recorded fork point that is no longer
// in its history, so the replay range would silently widen to include the
// base's own commits. Refusing is the only safe answer.
func TestRestackRefusesABranchThatMovedOffItsRecordedParent(t *testing.T) {
	restackRepo(t)
	trackStack(t)
	advanceTrunk(t, false)
	// Rebase synthetic-b by hand, exactly as a user might.
	gitOutput(t, "rebase", "--onto", "synthetic-trunk", "synthetic-a", "synthetic-b")

	stdout, _, err := run(t, "restack", "--scope", "graph", "--apply")
	if err == nil && !strings.Contains(stdout, "blocked") {
		t.Fatalf("restack did not refuse a branch that moved off its parent:\n%s", stdout)
	}
	if !strings.Contains(stdout, "retrack") && (err == nil || !strings.Contains(err.Error(), "retrack")) {
		t.Errorf("nothing named the remedy:\n%s\n%v", stdout, err)
	}
}

// Resume verbs act on an operation already under way and take nothing else.
func TestRestackResumeVerbsAreExclusive(t *testing.T) {
	restackRepo(t)

	if _, _, err := run(t, "restack", "--continue", "--abort"); err == nil {
		t.Error("--continue --abort was accepted")
	}
	if _, _, err := run(t, "restack", "--continue", "--apply"); err == nil {
		t.Error("--continue --apply was accepted")
	}
	if _, _, err := run(t, "restack", "--continue"); err == nil {
		t.Error("--continue was accepted with no restack in progress")
	}
}

// droppedCommitRepo records the stack while the parent still has two commits,
// then removes the parent's tip. The child keeps carrying it, so it is an
// orphan the restack has to decide about — and because it was removed rather
// than rewritten, keeping it is a coherent choice.
func droppedCommitRepo(t *testing.T) string {
	t.Helper()
	dir := restackRepo(t)
	gitOutput(t, "checkout", "-q", "synthetic-a")
	if err := os.WriteFile(filepath.Join(dir, "extra.txt"), []byte("extra\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitOutput(t, "add", "-A")
	gitOutput(t, "commit", "-qm", "a2")
	// Rebuild synthetic-b on top of the two-commit parent, then adopt both, so
	// the recorded fork point is the parent's tip at that moment.
	gitOutput(t, "checkout", "-q", "synthetic-b")
	gitOutput(t, "rebase", "-q", "--onto", "synthetic-a", "synthetic-a~1", "synthetic-b")
	trackStack(t)
	// The parent gives up its tip commit; synthetic-b still has it.
	gitOutput(t, "checkout", "-q", "synthetic-a")
	gitOutput(t, "reset", "-q", "--hard", "HEAD~1")
	gitOutput(t, "checkout", "-q", "synthetic-b")
	return dir
}

// A commit the parent dropped must never disappear from a child quietly, and
// whether keeping it is even coherent has to be said too.
func TestRestackReportsCommitsTheParentDropped(t *testing.T) {
	droppedCommitRepo(t)

	stdout, _, err := run(t, "restack", "--branch", "synthetic-b", "--scope", "path")
	if err != nil {
		t.Fatalf("restack: %v\n%s", err, stdout)
	}

	if !strings.Contains(stdout, "dropped") {
		t.Errorf("output does not report the dropped commit:\n%s", stdout)
	}
	if !strings.Contains(stdout, "--absorb") {
		t.Errorf("output does not offer to keep it:\n%s", stdout)
	}
}

// Keeping them rewrites nothing: the parent's tip is already an ancestor.
func TestRestackAbsorbRewritesNothing(t *testing.T) {
	droppedCommitRepo(t)
	before := gitOutput(t, "rev-parse", "synthetic-b")

	stdout, _, err := run(t, "restack", "--branch", "synthetic-b", "--scope", "path", "--absorb", "--apply")
	if err != nil {
		t.Fatalf("restack --absorb: %v\n%s", err, stdout)
	}

	if now := gitOutput(t, "rev-parse", "synthetic-b"); now != before {
		t.Errorf("--absorb rewrote synthetic-b (%s -> %s); it only re-records the fork point", before, now)
	}
	if !strings.Contains(stdout, "Nothing is rewritten") {
		t.Errorf("output does not say nothing was rewritten:\n%s", stdout)
	}
}

// Dropping is the default, and the commit really does go.
func TestRestackDropsOrphansByDefault(t *testing.T) {
	droppedCommitRepo(t)

	if stdout, _, err := run(t, "restack", "--branch", "synthetic-b", "--scope", "path", "--apply"); err != nil {
		t.Fatalf("restack --apply: %v\n%s", err, stdout)
	}

	if commits := gitOutput(t, "log", "--oneline", "synthetic-a..synthetic-b"); strings.Contains(commits, "a2") {
		t.Errorf("the dropped commit survived in synthetic-b:\n%s", commits)
	}
}

// Moving a fragment onto a different base has to record the move, not just
// perform it. Leaving the recorded parent naming the old base would make the
// graph describe a structure that no longer exists.
func TestRestackOntoRecordsTheNewParent(t *testing.T) {
	dir := restackRepo(t)
	gitOutput(t, "checkout", "-q", "synthetic-trunk")
	gitOutput(t, "checkout", "-qb", "synthetic-release")
	if err := os.WriteFile(filepath.Join(dir, "release.txt"), []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitOutput(t, "add", "-A")
	gitOutput(t, "commit", "-qm", "release work")
	gitOutput(t, "checkout", "-q", "synthetic-b")
	trackStack(t)

	stdout, _, err := run(t, "restack", "--branch", "synthetic-b", "--scope", "path", "--onto", "synthetic-release", "--apply")
	if err != nil {
		t.Fatalf("restack --onto: %v\n%s", err, stdout)
	}

	if !isAncestor(t, "synthetic-release", "synthetic-a") {
		t.Fatal("synthetic-a was not moved onto the new base")
	}
	// The graph must now agree, so a later command measures against reality.
	graph, _, err := run(t, "graph", "--branch", "synthetic-b", "--scope", "graph")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(graph, "synthetic-release") {
		t.Errorf("the graph does not record the new base:\n%s", graph)
	}
	for _, stale := range []string{"needs restack", "moved off parent"} {
		if strings.Contains(graph, stale) {
			t.Errorf("the graph reports %q after a completed restack:\n%s", stale, graph)
		}
	}
}

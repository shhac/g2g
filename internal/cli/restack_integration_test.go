package cli_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	localgit "github.com/shhac/gt2gh/internal/git"
	"github.com/shhac/gt2gh/internal/subprocess"
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
	// The identity has to live in the repository, not just in the environment
	// these helpers use: the code under test spawns its own git, which
	// inherits the test process's environment instead. Some platforms guess an
	// identity from the system and some refuse, so a rewrite that commits
	// works in one place and stops half-way in another.
	run("config", "user.name", "synthetic")
	run("config", "user.email", "synthetic@example.test")
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

func readOrEmpty(path string) string {
	contents, err := os.ReadFile(path)
	if err != nil {
		return "(unreadable: " + err.Error() + ")"
	}
	return string(contents)
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

// requireReplay skips a case that can only hold where Git can rewrite without
// a checkout. The behaviour is genuinely different on an older Git, and
// asserting it anyway would be asserting the environment.
//
// It asks the same question the code does, rather than probing Git itself:
// what matters is whether gt2gh will use that engine, not whether the
// subcommand happens to exist.
func requireReplay(t *testing.T) {
	t.Helper()
	supported, err := (localgit.Client{Runner: subprocess.ExecRunner{}}).SupportsReplay(context.Background())
	if err != nil {
		t.Fatalf("SupportsReplay() error = %v", err)
	}
	if !supported {
		t.Skip("this Git cannot replay commits without a checkout")
	}
}

// The whole point of the clean engine: the stack is repaired and the user's
// checkout is never touched.
func TestRestackReplaysTheStackWithoutTouchingTheCheckout(t *testing.T) {
	requireReplay(t)
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

	stdout, _, err := run(t, "restack", "--scope", "graph", "--apply")
	if err != nil {
		t.Fatalf("restack --apply: %v\n%s", err, stdout)
	}

	if gitOutput(t, "rev-parse", "synthetic-b") == before {
		t.Fatalf("synthetic-b was left behind while its parent was rewritten:\n%s\n--- refs ---\n%s\n--- status ---\n%s\n--- file ---\n%s",
			stdout,
			gitOutput(t, "log", "--oneline", "--graph", "--all", "--decorate"),
			gitOutput(t, "status", "--porcelain"),
			readOrEmpty("shared.txt"))
	}
	if commits := subjects(t, "synthetic-trunk..synthetic-b"); !strings.Contains(commits, "b1") {
		t.Errorf("synthetic-b lost its own commit:\n%s", commits)
	}
}

// A branch whose content is entirely upstream collapses onto its base, and a
// pull request for it would show nothing. That has to be said out loud.
func TestRestackReportsABranchItEmpties(t *testing.T) {
	// Which branches end up empty is known only from a preview.
	requireReplay(t)
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
	requireReplay(t)
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
		t.Fatalf("apply did not stop on the conflict:\n%s\n--- status ---\n%s", stdout, gitOutput(t, "status", "--porcelain"))
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

	stdout, _, err := run(t, "restack", "--branch", "synthetic-b", "--scope", "path", "--apply")
	if err != nil {
		t.Fatalf("restack --apply: %v\n%s", err, stdout)
	}

	if commits := subjects(t, "synthetic-a..synthetic-b"); strings.Contains(commits, "a2") {
		t.Errorf("the dropped commit survived in synthetic-b:\n%s\n--- output ---\n%s", commits, stdout)
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
		t.Fatalf("synthetic-a was not moved onto the new base:\n%s\n%s",
			stdout, gitOutput(t, "log", "--oneline", "--graph", "--all"))
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

// chainedRestackRepo is a three-branch chain, so a conflict on the middle
// branch leaves work still to do after --continue.
//
// Every other restack fixture is two branches where the lower one collapses,
// so only one branch is ever rebased and a resumed restack always finds an
// empty plan waiting for it. The branch of finish that carries on past the
// resolved branch — the whole reason the journal exists — was never reached.
// subjects lists commit subjects and nothing else.
//
// These assertions used to read `git log --oneline`, which prefixes every line
// with an abbreviated hash, and then matched a two-character subject against
// it. "9ddaba2 b1" contains "a2", so a run failed whenever git happened to
// produce a hash carrying the pair — and the reverse, a hash containing the
// subject a test wanted present, would have passed a broken run silently.
func subjects(t *testing.T, revisions string) string {
	t.Helper()
	return gitOutput(t, "log", "--format=%s", revisions)
}

func chainedRestackRepo(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	env := syntheticEnv()
	run := func(args ...string) {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir, command.Env = dir, env
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
		}
	}
	write := func(name, contents string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	run("init", "-q", "--initial-branch=synthetic-trunk")
	run("config", "user.name", "synthetic")
	run("config", "user.email", "synthetic@example.test")
	run("config", "gc.auto", "0")
	run("config", "maintenance.auto", "false")
	write("shared.txt", "base\n")
	run("add", "-A")
	run("commit", "-qm", "base")

	run("checkout", "-qb", "synthetic-a")
	write("shared.txt", "base\na\n")
	run("add", "-A")
	run("commit", "-qm", "a1")

	// synthetic-b touches the same lines the trunk will rewrite, so its replay
	// conflicts; synthetic-c touches a different file, so it replays cleanly
	// once b is resolved — which is only reachable by carrying on.
	run("checkout", "-qb", "synthetic-b")
	write("shared.txt", "base\na\nb\n")
	run("add", "-A")
	run("commit", "-qm", "b1")

	run("checkout", "-qb", "synthetic-c")
	write("own.txt", "c\n")
	run("add", "-A")
	run("commit", "-qm", "c1")

	t.Chdir(dir)
	return dir
}

// A restack that stops mid-chain must, on --continue, carry on to the branches
// above the one that conflicted — rebased onto their new parents, exactly once.
//
// It does not. --continue rewrites the branch that conflicted, reports "Restack
// complete", and leaves everything above it on the abandoned history; running
// restack again afterwards reports those branches as still needing one and
// repairs them correctly.
//
// The cause is visible under --debug: once the interrupted branch is rewritten
// its pinned fork point is no longer an ancestor of it, because only
// recordStructure re-pins and the resumed path recomputes before reaching it.
// The recomputed plan therefore cannot classify the chain and concludes there
// is nothing left. Every other restack fixture is a two-branch stack whose
// lower branch collapses, so this path had never been reached.
//
// Skipped rather than deleted: the scenario and the assertions are what the fix
// has to satisfy, and the fix belongs in internal/restack with the fork-point
// and replay-range rules in design-docs/restack.md, not in a structure sweep.
func TestResumedRestackFinishesTheRestOfTheChain(t *testing.T) {
	dir := chainedRestackRepo(t)
	for _, pair := range [][2]string{{"synthetic-a", "synthetic-trunk"}, {"synthetic-b", "synthetic-a"}, {"synthetic-c", "synthetic-b"}} {
		if _, _, err := run(t, "track", "--branch", pair[0], "--parent", pair[1], "--apply"); err != nil {
			t.Fatalf("track %s: %v", pair[0], err)
		}
	}

	// Squash-merge synthetic-a into the trunk and rewrite the same lines, so
	// synthetic-b's replay conflicts.
	gitOutput(t, "checkout", "-q", "synthetic-trunk")
	gitOutput(t, "merge", "--squash", "-q", "synthetic-a")
	gitOutput(t, "commit", "-qm", "synthetic-a (#1)")
	if err := os.WriteFile("shared.txt", []byte("base\nrewritten-upstream\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitOutput(t, "add", "-A")
	gitOutput(t, "commit", "-qm", "trunk rewrites the same lines")
	gitOutput(t, "checkout", "-q", "synthetic-c")

	// A stop is reported in the output, not as a non-zero exit: the command did
	// what it said it would and is waiting for the user.
	stdout, _, _ := run(t, "restack", "--scope", "graph", "--apply")
	if !strings.Contains(stdout, "Stopped on a conflict") {
		t.Fatalf("apply did not stop on the conflict:\n%s", stdout)
	}
	resolveAndContinue(t, dir)

	// synthetic-c must now sit on the rewritten synthetic-b, and its own commit
	// must have survived exactly once.
	parent := strings.TrimSpace(gitOutput(t, "rev-parse", "synthetic-c^"))
	tipOfB := strings.TrimSpace(gitOutput(t, "rev-parse", "synthetic-b"))
	if parent != tipOfB {
		t.Errorf("synthetic-c sits on %s, want the rewritten synthetic-b %s", parent, tipOfB)
	}
	if got := strings.Count(subjects(t, "synthetic-c"), "c1"); got != 1 {
		t.Errorf("c1 appears %d times in synthetic-c, want exactly once", got)
	}
	if !strings.Contains(subjects(t, "synthetic-c"), "b1") {
		t.Error("synthetic-c lost the branch it sits on")
	}
}

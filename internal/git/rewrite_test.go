package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/subprocess"
	"github.com/shhac/gt2gh/internal/testutil"
)

// stackRepo builds synthetic-trunk <- synthetic-a <- synthetic-b, where the
// trunk has since absorbed synthetic-a's work as one squashed commit and moved
// on. That is the shape every restack exists to repair, and a rewrite is the
// one thing a PATH fake cannot prove: the fake answers whatever it is asked,
// and the question here is what Git actually produces.
func stackRepo(t *testing.T, overlap bool) (Client, map[string]string) {
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
	write := func(name, contents string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(message string) {
		run("add", "-A")
		run("commit", "-qm", message)
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
	write("shared.txt", "base\n")
	commit("base")
	run("checkout", "-qb", "synthetic-a")
	write("shared.txt", "base\na\n")
	commit("a1")
	run("checkout", "-qb", "synthetic-b")
	write("shared.txt", "base\na\nb\n")
	commit("b1")

	tips := map[string]string{"forkPoint": run("rev-parse", "synthetic-a")}
	run("checkout", "-q", "synthetic-trunk")
	run("merge", "--squash", "-q", "synthetic-a")
	commit("synthetic-a (#1)")
	if overlap {
		// The trunk rewrites the very lines synthetic-b builds on, so the
		// replay cannot apply cleanly.
		write("shared.txt", "base\nrewritten-upstream\n")
		commit("trunk rewrites the same lines")
	}
	tips["trunk"] = run("rev-parse", "synthetic-trunk")
	tips["b"] = run("rev-parse", "synthetic-b")

	t.Chdir(dir)
	return Client{Runner: subprocess.ExecRunner{}}, tips
}

// requireReplay skips a case that only applies where the preview engine is
// used. Replay is gated to a verified version, so an older Git takes the
// resumable engine and these say nothing about it.
func requireReplay(t *testing.T, client Client) {
	t.Helper()
	supported, err := client.SupportsReplay(context.Background())
	if err != nil {
		t.Fatalf("SupportsReplay() error = %v", err)
	}
	if !supported {
		t.Skip("this Git is below the verified replay baseline")
	}
}

// The matrix is a table of strings, not five spawned processes: what is under
// test is the parsing, and a fake CLI answers whatever it is asked.
func TestParseGitVersionReadsTheMatrix(t *testing.T) {
	for version, want := range map[string][2]int{
		"git version 2.55.0":       {2, 55},
		"git version 2.44.0":       {2, 44},
		"git version 2.43.9":       {2, 43},
		"git version 3.0.0":        {3, 0},
		"git version 2.39.5 (Foo)": {2, 39},
	} {
		major, minor, err := parseGitVersion([]byte(version))
		if err != nil {
			t.Errorf("parseGitVersion(%q) error = %v", version, err)
			continue
		}
		if major != want[0] || minor != want[1] {
			t.Errorf("parseGitVersion(%q) = %d.%d, want %d.%d", version, major, minor, want[0], want[1])
		}
	}
	for _, malformed := range []string{"", "git", "git version", "git version x.y", "git version 2"} {
		if _, _, err := parseGitVersion([]byte(malformed)); err == nil {
			t.Errorf("parseGitVersion(%q) error = nil, want a refusal", malformed)
		}
	}
}

// One end-to-end case still runs the real adapter, because argv construction
// and exit handling are what a fake CLI does prove.
func TestSupportsReplayGatesOnTheVersionItParses(t *testing.T) {
	for version, want := range map[string]bool{
		"git version 2.44.0": true,
		"git version 2.43.9": false,
	} {
		t.Run(version, func(t *testing.T) {
			testutil.FakeCLIs(t, map[string][]testutil.Route{
				"git": {{Prefix: "--version", Output: version}},
			})
			got, err := (Client{Runner: subprocess.ExecRunner{}}).SupportsReplay(context.Background())
			if err != nil {
				t.Fatalf("SupportsReplay() error = %v", err)
			}
			if got != want {
				t.Errorf("SupportsReplay(%q) = %t, want %t", version, got, want)
			}
		})
	}
}

// A preview has to be free: exact object ids, and not one ref moved.
func TestPreviewReplayReportsUpdatesWithoutMovingAnything(t *testing.T) {
	client, tips := stackRepo(t, false)
	requireReplay(t, client)
	ctx := context.Background()

	updates, clean, err := client.PreviewReplay(ctx, "synthetic-trunk",
		[]Range{{From: tips["forkPoint"], To: "synthetic-b"}})
	if err != nil {
		t.Fatalf("PreviewReplay() error = %v", err)
	}
	if !clean {
		t.Fatal("clean = false for a rewrite that applies")
	}
	if len(updates) != 1 || updates[0].Branch() != "synthetic-b" {
		t.Fatalf("updates = %#v, want one for synthetic-b", updates)
	}
	if updates[0].New == updates[0].Old {
		t.Error("the preview reports no change for a branch that must move")
	}
	if after, _ := client.Resolve(ctx, "synthetic-b"); after != tips["b"] {
		t.Errorf("PreviewReplay moved synthetic-b to %s", after)
	}
}

// Predicting the conflict before touching anything is what lets the command
// ask before it takes over the working tree.
func TestPreviewReplayPredictsAConflictWithoutFailing(t *testing.T) {
	client, tips := stackRepo(t, true)
	requireReplay(t, client)

	_, clean, err := client.PreviewReplay(context.Background(), "synthetic-trunk",
		[]Range{{From: tips["forkPoint"], To: "synthetic-b"}})
	if err != nil {
		t.Fatalf("PreviewReplay() error = %v; a conflict is an answer, not a failure", err)
	}
	if clean {
		t.Error("clean = true for a rewrite that cannot apply")
	}
}

func TestReplayRewritesWithoutTouchingTheCheckout(t *testing.T) {
	client, tips := stackRepo(t, false)
	requireReplay(t, client)
	ctx := context.Background()
	head := currentBranch(t)

	if err := client.Replay(ctx, "synthetic-trunk", []Range{{From: tips["forkPoint"], To: "synthetic-b"}}); err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	if after, _ := client.Resolve(ctx, "synthetic-b"); after == tips["b"] {
		t.Error("Replay did not move synthetic-b")
	}
	built, err := client.IsAncestor(ctx, "synthetic-trunk", "synthetic-b")
	if err != nil || !built {
		t.Errorf("synthetic-b is not built on the trunk after the replay (%v)", err)
	}
	if now := currentBranch(t); now != head {
		t.Errorf("HEAD moved from %q to %q", head, now)
	}
}

// A conflicting replay must leave the repository exactly as it found it, which
// is why it is safe to try first.
func TestReplayLeavesNothingBehindOnConflict(t *testing.T) {
	client, tips := stackRepo(t, true)
	requireReplay(t, client)
	ctx := context.Background()

	err := client.Replay(ctx, "synthetic-trunk", []Range{{From: tips["forkPoint"], To: "synthetic-b"}})
	if err == nil {
		t.Fatal("Replay() error = nil for a conflicting rewrite")
	}
	if after, _ := client.Resolve(ctx, "synthetic-b"); after != tips["b"] {
		t.Errorf("synthetic-b moved to %s despite the conflict", after)
	}
	inProgress, err := client.RebaseInProgress(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if inProgress {
		t.Error("a conflicting replay left in-progress state to clean up")
	}
}

// The resumable engine: stop on the conflict, then continue from a separate
// invocation once it is resolved.
func TestRebaseStopsAndResumesAcrossInvocations(t *testing.T) {
	client, tips := stackRepo(t, true)
	ctx := context.Background()

	if err := client.Rebase(ctx, "synthetic-trunk", Range{From: tips["forkPoint"], To: "synthetic-b"}); err == nil {
		t.Fatal("Rebase() error = nil for a conflicting rewrite")
	}
	inProgress, err := client.RebaseInProgress(ctx)
	if err != nil || !inProgress {
		t.Fatalf("RebaseInProgress() = %t, %v; want an interrupted rebase", inProgress, err)
	}

	if err := os.WriteFile("shared.txt", []byte("base\nrewritten-upstream\nb\nresolved\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stage(t, "shared.txt")
	if err := client.RebaseContinue(ctx); err != nil {
		t.Fatalf("RebaseContinue() error = %v", err)
	}

	if inProgress, _ := client.RebaseInProgress(ctx); inProgress {
		t.Error("the rebase is still in progress after continuing")
	}
	built, err := client.IsAncestor(ctx, "synthetic-trunk", "synthetic-b")
	if err != nil || !built {
		t.Errorf("synthetic-b is not on the trunk after the resolved rebase (%v)", err)
	}
}

// Abort has to restore every branch the rebase touched, which is what lets the
// journal carry only what spans several invocations.
func TestRebaseAbortRestoresTheBranch(t *testing.T) {
	client, tips := stackRepo(t, true)
	ctx := context.Background()
	if err := client.Rebase(ctx, "synthetic-trunk", Range{From: tips["forkPoint"], To: "synthetic-b"}); err == nil {
		t.Fatal("expected a conflict")
	}

	if err := client.RebaseAbort(ctx); err != nil {
		t.Fatalf("RebaseAbort() error = %v", err)
	}

	if after, _ := client.Resolve(ctx, "synthetic-b"); after != tips["b"] {
		t.Errorf("synthetic-b = %s after abort, want the original %s", after, tips["b"])
	}
	if inProgress, _ := client.RebaseInProgress(ctx); inProgress {
		t.Error("abort left the rebase in progress")
	}
}

func TestResolveRejectsWhatIsNotACommitHere(t *testing.T) {
	client, tips := stackRepo(t, false)
	ctx := context.Background()

	if got, err := client.Resolve(ctx, "synthetic-trunk"); err != nil || got != tips["trunk"] {
		t.Errorf("Resolve(trunk) = %q, %v", got, err)
	}
	if _, err := client.Resolve(ctx, "0000000000000000000000000000000000000000"); err == nil {
		t.Error("Resolve() error = nil for an object that is not here")
	}
}

// The fork point is the one object a restack cannot do without, and it becomes
// unreachable exactly when it matters. A ref keeps it collectable-proof.
func TestForkPointPinSurvivesAggressiveCollection(t *testing.T) {
	client, tips := stackRepo(t, false)
	ctx := context.Background()
	if err := client.PinForkPoint(ctx, "synthetic-b", tips["forkPoint"]); err != nil {
		t.Fatalf("PinForkPoint() error = %v", err)
	}
	// Remove every other way of reaching it, then collect.
	gitExec(t, "branch", "-qD", "synthetic-a")
	gitExec(t, "reflog", "expire", "--expire=now", "--all")
	gitExec(t, "gc", "--prune=now", "-q")

	if _, err := client.Resolve(ctx, tips["forkPoint"]); err != nil {
		t.Fatalf("the pinned fork point was collected: %v", err)
	}
	if err := client.UnpinForkPoint(ctx, "synthetic-b"); err != nil {
		t.Fatalf("UnpinForkPoint() error = %v", err)
	}
	if err := client.UnpinForkPoint(ctx, "synthetic-b"); err != nil {
		t.Errorf("unpinning twice should be harmless, got %v", err)
	}
}

// Absorbing a rewritten commit would give a child a stale duplicate of work
// its parent still carries, so the two cases must be told apart by content.
func TestCherryDroppedSeparatesDroppedFromRewritten(t *testing.T) {
	client, _ := stackRepo(t, false)
	ctx := context.Background()
	gitExec(t, "checkout", "-q", "synthetic-a")
	if err := os.WriteFile("extra.txt", []byte("extra\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stage(t, "extra.txt")
	gitExec(t, "commit", "-qm", "a2")
	before := revisionOf(t, "synthetic-a")
	// Drop a2 but keep a1, so the parent has one genuinely dropped commit.
	gitExec(t, "reset", "-q", "--hard", "HEAD~1")

	dropped, err := client.CherryDropped(ctx, "synthetic-a", before)
	if err != nil {
		t.Fatalf("CherryDropped() error = %v", err)
	}
	if len(dropped) != 1 {
		t.Fatalf("dropped = %v, want exactly the one commit removed", dropped)
	}
}

func TestRewriteRejectsUnsafeRefNames(t *testing.T) {
	client := Client{Runner: subprocess.ExecRunner{}}
	ctx := context.Background()

	if err := client.Replay(ctx, "-synthetic", []Range{{From: "a", To: "b"}}); err == nil {
		t.Error("Replay() error = nil for an option-like base")
	}
	if err := client.Replay(ctx, "synthetic", nil); err == nil {
		t.Error("Replay() error = nil with no ranges")
	}
	if err := client.Rebase(ctx, "synthetic", Range{From: "-a", To: "b"}); err == nil {
		t.Error("Rebase() error = nil for an option-like range")
	}
	if err := client.PinForkPoint(ctx, "-synthetic", "abc"); err == nil {
		t.Error("PinForkPoint() error = nil for an option-like branch")
	}
}

// Only the recorded argv proves --update-refs was sent, which is what moves
// the intermediate branches of a stack rather than just its tip.
func TestRebaseRequestsUpdateRefs(t *testing.T) {
	recorder := testutil.FakeCLIs(t, map[string][]testutil.Route{"git": {{Prefix: "rebase"}}})

	if err := (Client{Runner: subprocess.ExecRunner{}}).Rebase(context.Background(), "synthetic-trunk",
		Range{From: "synthetic-fork", To: "synthetic-b"}); err != nil {
		t.Fatal(err)
	}

	recorder.Find("git rebase --onto synthetic-trunk synthetic-fork synthetic-b --no-reapply-cherry-picks --empty=drop")
}

func TestRebaseStepsSuppressTheEditor(t *testing.T) {
	recorder := testutil.FakeCLIs(t, map[string][]testutil.Route{"git": {{Prefix: "-c core.editor=true rebase"}}})
	client := Client{Runner: subprocess.ExecRunner{}}
	ctx := context.Background()

	for name, step := range map[string]func(context.Context) error{
		"--continue": client.RebaseContinue,
		"--abort":    client.RebaseAbort,
		"--skip":     client.RebaseSkip,
	} {
		if err := step(ctx); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		recorder.Find("git -c core.editor=true rebase " + name)
	}
}

func currentBranch(t *testing.T) string {
	t.Helper()
	return gitExec(t, "branch", "--show-current")
}

func revisionOf(t *testing.T, ref string) string {
	t.Helper()
	return gitExec(t, "rev-parse", ref)
}

func stage(t *testing.T, path string) {
	t.Helper()
	gitExec(t, "add", path)
}

func gitExec(t *testing.T, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Env = syntheticEnv()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

// fastForwardRepo is a trunk and a branch that has moved past it, so a
// fast-forward is legitimate — plus a way to make the two diverge.
func fastForwardRepo(t *testing.T) (string, Client) {
	t.Helper()

	repo := testutil.NewGitRepo(t, "synthetic-trunk")
	repo.Commit("synthetic root", "root.txt", "root")
	repo.Run("checkout", "-b", "synthetic-ahead")
	repo.Commit("synthetic ahead", "ahead.txt", "ahead")
	repo.Run("checkout", "synthetic-trunk")
	return repo.Dir, Client{Runner: subprocess.ExecRunner{}}
}

// The trunk-advance sync performs goes through here, and its only caller fakes
// it, so nothing had ever run this against real git.
func TestFastForwardAdvancesABranchThatIsBehind(t *testing.T) {
	dir, client := fastForwardRepo(t)
	t.Chdir(dir)

	if err := client.FastForward(context.Background(), "synthetic-trunk", "synthetic-ahead"); err != nil {
		t.Fatalf("FastForward() error = %v", err)
	}

	trunk, err := client.Resolve(context.Background(), "synthetic-trunk")
	if err != nil {
		t.Fatal(err)
	}
	ahead, err := client.Resolve(context.Background(), "synthetic-ahead")
	if err != nil {
		t.Fatal(err)
	}
	if trunk != ahead {
		t.Errorf("trunk = %s, want it advanced to %s", trunk, ahead)
	}
}

// "You are behind" and "you have diverged" want different responses, and only
// the user can give the second. Advancing anyway would discard their commits.
func TestFastForwardRefusesADivergedBranch(t *testing.T) {
	dir, client := fastForwardRepo(t)
	t.Chdir(dir)

	// Give the trunk a commit of its own, so neither contains the other.
	if err := os.WriteFile(filepath.Join(dir, "local.txt"), []byte("local"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitExec(t, "add", "-A")
	gitExec(t, "commit", "-m", "synthetic local")
	before, err := client.Resolve(context.Background(), "synthetic-trunk")
	if err != nil {
		t.Fatal(err)
	}

	err = client.FastForward(context.Background(), "synthetic-trunk", "synthetic-ahead")
	if err == nil {
		t.Fatal("FastForward() error = nil for a diverged branch")
	}
	after, resolveErr := client.Resolve(context.Background(), "synthetic-trunk")
	if resolveErr != nil {
		t.Fatal(resolveErr)
	}
	if after != before {
		t.Errorf("a refused fast-forward moved the branch from %s to %s", before, after)
	}
}

// Already level is a no-op, not an error: sync asks for this every time the
// remote has not moved.
func TestFastForwardIsANoOpWhenAlreadyLevel(t *testing.T) {
	dir, client := fastForwardRepo(t)
	t.Chdir(dir)

	if err := client.FastForward(context.Background(), "synthetic-trunk", "synthetic-trunk"); err != nil {
		t.Errorf("FastForward() error = %v for a branch already at the target", err)
	}
}

// syntheticEnv is the shared environment every throwaway repository runs
// under. It lives in testutil because internal/cli needs the identical thing
// and the two copies were byte-for-byte the same.
func syntheticEnv() []string { return testutil.SyntheticGitEnv() }

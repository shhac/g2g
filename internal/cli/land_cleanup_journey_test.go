package cli_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/cli"
)

// A reviewer can push to a branch after its pull request merged, and that
// commit is on the remote alone. Land deletes a remote branch only while it
// still holds the head that landed, so the commit survives the cleanup, which
// says it could not delete and lets the descent stand.

// heldTrunkLeaf is one recorded branch on main, published, with main checked
// out in another worktree. Landing it with --worktrees skip leaves that trunk
// where it is, so nothing pulls the branch's remote version back here: the
// cleanup is all that stands between a post-merge commit and the delete.
func heldTrunkLeaf(t *testing.T, w *world) []string {
	t.Helper()
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	w.git(w.Local, "push", "-q", "origin", "synthetic-a")
	w.git(w.Local, "worktree", "add", "-q", filepath.Join(t.TempDir(), "synthetic-held"), "main")
	return []string{"land", "--branch", "synthetic-a", "--no-comment", "--worktrees", "skip", "--apply"}
}

// assertReviewKept checks the reviewer's commit is still the remote branch and
// that land said why it left it.
func assertReviewKept(t *testing.T, w *world, state, stderr string) {
	t.Helper()
	reviewed := readState(t, state, "review-41.commit")
	if reviewed == "" {
		t.Fatal("the reviewer's commit was never pushed; the fake did not arrange the scenario")
	}
	if got := w.tip(w.Remote, "refs/heads/synthetic-a"); got != reviewed {
		t.Errorf("remote synthetic-a = %q, want the reviewer's commit %s kept", got, reviewed[:8])
	}
	if !strings.Contains(stderr, "could not delete synthetic-a on origin") {
		t.Errorf("stderr does not report the cleanup it skipped:\n%s", stderr)
	}
}

func TestJourneyLandKeepsACommitPushedAfterItsMerge(t *testing.T) {
	w := newWorld(t)
	land := heldTrunkLeaf(t, w)
	state := landingGitHubStack(t, w.Remote, "synthetic-a")
	flag(t, state, "review-on-merge-41", "synthetic-a")

	stdout, stderr, err := run(t, land...)
	if err != nil {
		t.Fatalf("land: %v, want a skipped cleanup to leave the descent standing\n%s%s", err, stdout, stderr)
	}
	if got := readState(t, state, "pr-41.state"); got != "MERGED" {
		t.Fatalf("pull request #41 state = %q, want MERGED", got)
	}
	assertReviewKept(t, w, state, stderr)
	if strings.Contains(w.readStore(), "synthetic-a") {
		t.Errorf("the landed branch is still recorded:\n%s", w.readStore())
	}
	w.assertClean(w.Local)
}

// The pull request merged outside land, and the reviewer pushed after it, all
// before land was run to clean up. The plan's own read of the remote already
// holds that commit, so taking it as the lease would delete it. Here push's
// refusal, consulted once before the descent, stops it first: nothing is
// forgotten or deleted, here or there. The lease behind it is pinned in
// internal/land, for a remote that moves once that check has passed.
func TestJourneyLandCleanupAfterAMergeElsewhereKeepsALaterCommit(t *testing.T) {
	w := newWorld(t)
	land := heldTrunkLeaf(t, w)
	state := landingGitHubStack(t, w.Remote, "synthetic-a")
	flag(t, state, "review-on-merge-41", "synthetic-a")
	merged := w.tip(w.Local, "synthetic-a")
	if output, err := exec.Command("gh", "pr", "merge", "41", "--squash", "--match-head-commit", merged).CombinedOutput(); err != nil {
		t.Fatalf("merging #41 elsewhere: %v\n%s", err, output)
	}
	reviewed := readState(t, state, "review-41.commit")
	if reviewed == "" || w.tip(w.Remote, "refs/heads/synthetic-a") != reviewed {
		t.Fatal("the reviewer's commit did not reach the remote before land ran")
	}

	stdout, stderr, err := run(t, land...)
	if err == nil || cli.StoppedPartWayForTest(err) {
		t.Fatalf("land: err = %v, want a refusal that changed nothing\n%s%s", err, stdout, stderr)
	}
	if got := w.tip(w.Remote, "refs/heads/synthetic-a"); got != reviewed {
		t.Errorf("remote synthetic-a = %q, want the reviewer's commit %s kept", got, reviewed[:8])
	}
	if got := w.tip(w.Local, "synthetic-a"); got != merged {
		t.Errorf("local synthetic-a = %q, want it left at %s", got, merged[:8])
	}
	if !strings.Contains(w.readStore(), "synthetic-a") {
		t.Errorf("synthetic-a was forgotten by a run that refused:\n%s", w.readStore())
	}
	w.assertClean(w.Local)
}

// interruptedDescent lands synthetic-a and stops on the replay of synthetic-b
// above it, which conflicts with the trunk, then resolves that replay. The
// replay collapsed synthetic-a here onto the trunk while the remote still holds
// the head that merged, so the rerun is a cleanup with no merge of its own to
// pin the deletion to. It answers with that merged head.
func interruptedDescent(t *testing.T, w *world) (string, []string) {
	t.Helper()
	w.commit(w.Local, "main", "b.txt", "base")
	w.git(w.Local, "push", "-q", "origin", "main")
	// One commit, whose squash is equivalent to it: a several-commit branch
	// with no sync point is refused by push's preflight on the rerun, before
	// cleanup is ever in question.
	w.branchOff("main", "synthetic-a", "a.txt")
	w.branchOff("synthetic-a", "synthetic-b", "b-own.txt")
	w.commit(w.Local, "synthetic-b", "b.txt", "from b")
	for _, edge := range [][2]string{{"synthetic-a", "main"}, {"synthetic-b", "synthetic-a"}} {
		mustRun(t, "track", "--branch", edge[0], "--parent", edge[1], "--apply")
	}
	w.git(w.Local, "push", "-q", "origin", "synthetic-a", "synthetic-b")
	w.commit(w.Local, "main", "b.txt", "from the trunk")
	w.git(w.Local, "push", "-q", "origin", "main")
	w.git(w.Local, "switch", "-q", "synthetic-a")
	merged := w.tip(w.Remote, "synthetic-a")
	state := landingGitHubStack(t, w.Remote, "synthetic-a", "synthetic-b")

	land := []string{"land", "--branch", "synthetic-a", "--scope", "path", "--no-comment", "--apply"}
	stdout, _, err := run(t, land...)
	if !cli.StoppedPartWayForTest(err) {
		t.Fatalf("land error = %v, want it stopped part-way on the conflict:\n%s", err, stdout)
	}
	if got := readState(t, state, "pr-41.state"); got != "MERGED" {
		t.Fatalf("pull request #41 state = %q, want MERGED", got)
	}
	w.resolveRestack(w.Local)
	if w.tip(w.Local, "synthetic-a") == merged || w.tip(w.Remote, "synthetic-a") != merged {
		t.Fatal("the interrupted descent did not leave synthetic-a collapsed here and merged on the remote")
	}
	return merged, land
}

// The ordinary rerun still cleans up: push's preflight reads the merged head
// as commits this clone dropped, which is evidence it holds nothing of the
// remote's own.
func TestJourneyLandRerunAfterAnInterruptedDescentDeletesTheMergedHead(t *testing.T) {
	w := newWorld(t)
	_, land := interruptedDescent(t, w)

	mustRun(t, land...)
	if remote := w.git(w.Local, "ls-remote", "--heads", "origin", "synthetic-a"); remote != "" {
		t.Errorf("the rerun left synthetic-a on the remote: %s", remote)
	}
	w.assertClean(w.Local)
}

// A reviewer reverts part of the merged work on the branch after its merge.
// The revert merges into the squashed trunk without changing it, so the
// whole-branch content check reads it as landed; it is on the remote alone.
func TestJourneyLandRerunKeepsARevertPushedAfterTheMerge(t *testing.T) {
	w := newWorld(t)
	merged, land := interruptedDescent(t, w)
	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "switch", "-q", "-c", "synthetic-a", "origin/synthetic-a")
	w.git(w.Other, "revert", "--no-edit", "HEAD")
	w.git(w.Other, "push", "-q", "origin", "synthetic-a")
	reverted := w.tip(w.Remote, "synthetic-a")
	if reverted == merged {
		t.Fatal("the revert did not reach the remote")
	}

	stdout, stderr, _ := run(t, land...)
	if got := w.tip(w.Remote, "synthetic-a"); got != reverted {
		t.Errorf("remote synthetic-a = %q, want the reviewer's revert %s kept\n%s%s", got, reverted[:8], stdout, stderr)
	}
	w.assertClean(w.Local)
}

// The ordinary descent never reaches the delete with such a commit: its pull
// takes the commit into the branch here, and the branch then holds work the
// trunk does not, so land stops before forgetting or deleting anything of it.
func TestJourneyLandStopsBeforeDeletingABranchAReviewerExtendedAfterItsMerge(t *testing.T) {
	w := newWorld(t)
	twoBranchStack(t, w)
	state := landingGitHub(t, w.Remote)
	flag(t, state, "review-on-merge-41", "synthetic-a")

	stdout, stderr, err := run(t, "land", "--apply")
	if !cli.StoppedPartWayForTest(err) {
		t.Fatalf("land: err = %v, want the part-way status\n%s%s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, "does not find its work in main") {
		t.Errorf("land did not stop for the work left on synthetic-a:\n%s", stdout)
	}
	reviewed := readState(t, state, "review-41.commit")
	if reviewed == "" {
		t.Fatal("the reviewer's commit was never pushed; the fake did not arrange the scenario")
	}
	if got := w.tip(w.Remote, "refs/heads/synthetic-a"); got != reviewed {
		t.Errorf("remote synthetic-a = %q, want the reviewer's commit %s kept", got, reviewed[:8])
	}
	w.assertClean(w.Local)
}

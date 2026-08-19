package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/testutil"
)

// These are journeys, not command checks: a person works on a stack while the
// remote moves under them, and each one runs to the point where they are either
// finished or told what to do next.
//
// Everything here is real except GitHub. Real Git, a real bare remote, and a
// real second clone standing in for a colleague. Every mutation is followed by
// a clean-tree assertion, because the failure that keeps recurring is a ref
// moving without the working tree following it.

// The ordinary day: work on a stack, the trunk moves upstream, bring it up to
// date. Nothing exotic, and it had never been run end to end — sync's only
// tests were injected fakes.
func TestJourneyTrunkMovesUpstreamWhileYouWork(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a", "--apply")

	// A colleague lands something on the trunk.
	w.commit(w.Other, "main", "colleague.txt", "theirs")
	w.git(w.Other, "push", "-q", "origin", "main")

	w.git(w.Local, "switch", "-q", "synthetic-b")
	mustRun(t, "sync", "--apply")

	w.assertClean(w.Local)
	if local, remote := w.tip(w.Local, "main"), w.tip(w.Other, "main"); local != remote {
		t.Errorf("trunk was not advanced: local %s, remote %s", local, remote)
	}
	for _, branch := range []string{"synthetic-a", "synthetic-b"} {
		if !w.contains(w.Local, "main", branch) {
			t.Errorf("%s was not replayed onto the advanced trunk", branch)
		}
	}
	w.assertHas(w.Local, "synthetic-a", "a.txt")
	w.assertHas(w.Local, "synthetic-b", "b.txt")
	// The colleague's work is in your stack now, which is the point of syncing.
	w.assertHas(w.Local, "synthetic-b", "colleague.txt")
}

// Somebody rewrote the trunk. Nothing g2g does can reconcile that safely, so
// the journey ends in a refusal that says so — with the repository untouched,
// which is the part worth proving.
func TestJourneyTheTrunkWasRewrittenUpstream(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")

	// You have a trunk commit of your own that never went out...
	w.commit(w.Local, "main", "yours.txt", "yours")
	// ...and the trunk upstream moved somewhere else entirely. Neither side is
	// an ancestor of the other, which is what diverged actually means: a trunk
	// that has only moved ahead still fast-forwards.
	w.commit(w.Other, "main", "theirs.txt", "theirs")
	w.git(w.Other, "push", "-q", "origin", "main")
	w.git(w.Local, "switch", "-q", "synthetic-a")

	before := w.tip(w.Local, "synthetic-a")
	_, _, err := run(t, "sync", "--apply")

	if err == nil {
		t.Fatal("sync reconciled a diverged trunk instead of refusing")
	}
	if !strings.Contains(err.Error(), "diverged") {
		t.Errorf("refusal does not say what is wrong: %v", err)
	}
	if after := w.tip(w.Local, "synthetic-a"); after != before {
		t.Errorf("a refused sync moved synthetic-a from %s to %s", before, after)
	}
	w.assertClean(w.Local)
}

// Publishing, then publishing again after more work. The second push is the
// one that matters: the preview has to say what it would send, and the refs
// have to actually arrive.
func TestJourneyPublishAStackAndThenAddToIt(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")

	mustRun(t, "push", "--apply")
	if local, remote := w.tip(w.Local, "synthetic-a"), w.tip(w.Remote, "synthetic-a"); local != remote {
		t.Fatalf("the branch did not reach the remote: local %s, remote %s", local, remote)
	}

	stdout, _, err := run(t, "push")
	if err != nil {
		t.Fatalf("push preview: %v\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "up to date") {
		t.Errorf("preview does not say the remote already has it:\n%s", stdout)
	}

	w.commit(w.Local, "synthetic-a", "more.txt", "more")
	stdout, _, err = run(t, "push")
	if err != nil {
		t.Fatalf("push preview: %v\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "1 commit to publish") {
		t.Errorf("preview does not say what it would send:\n%s", stdout)
	}
	mustRun(t, "push", "--apply")
	if local, remote := w.tip(w.Local, "synthetic-a"), w.tip(w.Remote, "synthetic-a"); local != remote {
		t.Errorf("the second push did not arrive: local %s, remote %s", local, remote)
	}
	w.assertClean(w.Local)
}

// You and a colleague both moved the same branch. The lease is what stops one
// of you overwriting the other, and the refusal has to arrive before the push
// rather than as a git error after it.
func TestJourneyAColleagueMovedYourBranch(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "push", "--apply")

	// They pull it, add to it, and publish before you do.
	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "switch", "-q", "-c", "synthetic-a", "origin/synthetic-a")
	w.commit(w.Other, "synthetic-a", "theirs.txt", "theirs")
	w.git(w.Other, "push", "-q", "origin", "synthetic-a")
	theirs := w.tip(w.Other, "synthetic-a")

	w.commit(w.Local, "synthetic-a", "yours.txt", "yours")
	stdout, _, err := run(t, "push", "--apply")

	if err == nil {
		t.Fatal("push overwrote a branch the remote had moved")
	}
	if !strings.Contains(stdout+err.Error(), "remote has moved") {
		t.Errorf("refusal does not say the remote moved:\n%s\n%v", stdout, err)
	}
	if now := w.tip(w.Remote, "synthetic-a"); now != theirs {
		t.Errorf("the remote branch changed from %s to %s", theirs, now)
	}
}

// Standing on a branch that is about to be rewritten. This is the bug fixed in
// v0.21.1: the ref moved, the working tree did not follow, and git status
// reported changes nobody made — which then blocked the next git switch.
func TestJourneyRestackingTheBranchYouAreStandingOn(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")

	w.commit(w.Local, "main", "moved.txt", "moved")
	w.git(w.Local, "switch", "-q", "synthetic-a")

	mustRun(t, "restack", "--apply")

	w.assertClean(w.Local)
	// The file the trunk added has to be in the working tree, not merely in the
	// commit: the whole failure was a tree describing the commit before.
	if _, err := os.Stat(filepath.Join(w.Local, "moved.txt")); err != nil {
		t.Errorf("the trunk's file is not in the working tree: %v", err)
	}
	// And you can leave, which you could not before.
	w.git(w.Local, "switch", "-q", "main")
	w.assertClean(w.Local)
}

// A conflict mid-rewrite. The journey is: it stops, it says so, and --abort
// puts everything back exactly where it was.
func TestJourneyARestackThatConflictsCanBeAbandoned(t *testing.T) {
	w := newWorld(t)
	w.git(w.Local, "switch", "-qc", "synthetic-a")
	w.commit(w.Local, "synthetic-a", "shared.txt", "branch version")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")

	// The trunk edits the same file, so replaying the branch must conflict.
	w.commit(w.Local, "main", "shared.txt", "trunk version")
	w.git(w.Local, "switch", "-q", "synthetic-a")
	before := w.tip(w.Local, "synthetic-a")

	stdout, _, _ := run(t, "restack", "--apply")
	if !strings.Contains(stdout, "conflict") {
		t.Fatalf("a conflicting rewrite did not report a conflict:\n%s", stdout)
	}

	mustRun(t, "restack", "--abort")
	if after := w.tip(w.Local, "synthetic-a"); after != before {
		t.Errorf("abort left synthetic-a at %s, want %s", after, before)
	}
	w.assertClean(w.Local)
}

// mustRun runs a command and fails the test with its output if it errors,
// because a journey that cannot take its next step has nothing left to assert.
func mustRun(t *testing.T, args ...string) string {
	t.Helper()
	stdout, stderr, err := run(t, args...)
	if err != nil {
		t.Fatalf("g2g %s: %v\n%s%s", strings.Join(args, " "), err, stdout, stderr)
	}
	return stdout
}

// fakeGitHub gives the world a GitHub without a network. Git stays real: only
// gh is answered from a table, because there is no local stand-in for a
// service and every claim about refs is still made against real Git.
func (w *world) fakeGitHub(pullRequests string) *testutil.Recorder {
	w.t.Helper()
	return testutil.FakeCLIs(w.t, map[string][]testutil.Route{
		"gh": {
			{Prefix: "repo view", Output: `{"nameWithOwner":"example/synthetic"}`},
			{Prefix: "api graphql", Output: pullRequests},
			{Prefix: "pr create"},
			{Prefix: "stack link"},
		},
	})
}

const noPullRequests = `{"data":{"repository":{"pr0":{"nodes":[]},"pr1":{"nodes":[]},"pr2":{"nodes":[]},"pr3":{"nodes":[]}}}}`

// Publishing a stack for the first time. submit pushes the refs itself, so the
// half that matters here is that they actually arrive — which no test checked,
// because submit had only ever pushed to a PATH fake.
func TestJourneySubmitPublishesTheRefsItCreatesPullRequestsFor(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a", "--apply")
	recorder := w.fakeGitHub(noPullRequests)

	specDir := t.TempDir()
	mustRun(t, "submit", "--write-spec", specDir)
	spec := filepath.Join(specDir, "submission.json")
	fillSpecTitles(t, spec)
	mustRun(t, "submit", "--spec", spec, "--apply")

	// The refs are real, so this is checkable against the real remote.
	for _, branch := range []string{"synthetic-a", "synthetic-b"} {
		if local, remote := w.tip(w.Local, branch), w.tip(w.Remote, branch); local != remote {
			t.Errorf("%s did not reach the remote: local %s, remote %s", branch, local, remote)
		}
	}
	// One pull request per branch, and the stack linked once.
	if got := strings.Count(strings.Join(recorder.Calls(), "\n"), "pr create"); got != 2 {
		t.Errorf("created %d pull requests, want one per branch", got)
	}
	w.assertClean(w.Local)
}

// The recovery this tool exists for: your parent was squash-merged upstream and
// deleted, so your branch carries commits whose content is already in the trunk
// and hangs from something that no longer exists.
func TestJourneyYourParentWasSquashMergedAndDeleted(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a", "--apply")
	mustRun(t, "push", "--apply")

	// The colleague squash-merges synthetic-a into the trunk: one new commit
	// carrying its content, under a different object id, and the branch gone.
	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "switch", "-q", "main")
	w.git(w.Other, "merge", "-q", "--squash", "origin/synthetic-a")
	w.git(w.Other, "commit", "-qm", "synthetic squash of a")
	w.git(w.Other, "push", "-q", "origin", "main")

	w.git(w.Local, "switch", "-q", "synthetic-b")
	mustRun(t, "sync", "--apply")

	w.assertClean(w.Local)
	if !w.contains(w.Local, "main", "synthetic-b") {
		t.Error("synthetic-b was not brought onto the squashed trunk")
	}
	// Its own work survives, and the parent's content is not duplicated: the
	// squashed commit is already in the trunk, so replaying it again would put
	// a.txt's change in twice.
	w.assertHas(w.Local, "synthetic-b", "b.txt")
	if own := w.git(w.Local, "rev-list", "--count", "main..synthetic-b"); own != "1" {
		t.Errorf("synthetic-b has %s commits above the trunk, want 1: the squashed parent was replayed again", own)
	}
}

package cli_test

import (
	"maps"
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

// remote history reverter: somebody rewrote the trunk and force-pushed it,
// which is what a rebase or a squash cleanup of the trunk looks like.
//
// Everything the local trunk has is in the published one under different object
// ids, so taking theirs loses nothing: the trunk is replaced and the stack is
// replayed onto it. This used to refuse, which left no way through at all.
func TestJourneyTheTrunkWasRewrittenAndHasEverythingYouHave(t *testing.T) {
	w := newWorld(t)
	// A trunk commit that then gets rewritten upstream, so both sides carry its
	// content under different ids.
	w.commit(w.Local, "main", "shared-work.txt", "shared")
	w.git(w.Local, "push", "-q", "origin", "main")
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")

	// Upstream, the trunk is rebuilt: same content, new commit.
	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "switch", "-q", "main")
	w.git(w.Other, "reset", "-q", "--hard", "origin/main")
	w.git(w.Other, "commit", "-q", "--amend", "-m", "synthetic rewritten trunk")
	w.git(w.Other, "push", "-q", "--force", "origin", "main")
	theirs := w.tip(w.Other, "main")

	w.git(w.Local, "switch", "-q", "synthetic-a")
	stdout := mustRun(t, "sync", "--apply")

	if !strings.Contains(stdout, "rewritten") {
		t.Errorf("the preview does not say the trunk was replaced:\n%s", stdout)
	}
	if now := w.tip(w.Local, "main"); now != theirs {
		t.Errorf("main is at %s, want the published %s", now, theirs)
	}
	if !w.contains(w.Local, "main", "synthetic-a") {
		t.Error("the stack was not replayed onto the replaced trunk")
	}
	w.assertHas(w.Local, "synthetic-a", "a.txt")
	w.assertClean(w.Local)
}

// The refusal is still there for the case that would cost something: the
// rewritten trunk does not have what this one does.
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
	if !strings.Contains(err.Error(), "both sides have moved") {
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

// --- Scenarios recorded rather than wished for -------------------------------
//
// The tests below assert what g2g does today. Where that is not what we would
// want, the comment says so and the assertion still pins the current answer, so
// changing it is a visible decision rather than a silent drift.

// friendly-fixer: a reviewer pushes a fix straight onto a branch you own.
//
// There used to be no way to collect it. sync fetched exactly one ref — the
// base — so a branch of yours was never brought down, and push then refused
// because the remote was ahead. Their commit was unreachable from here.
func TestJourneyAReviewerPushesToYourBranch(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "push", "--apply")

	// The reviewer fixes something on your branch.
	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "switch", "-q", "-c", "synthetic-a", "origin/synthetic-a")
	w.commit(w.Other, "synthetic-a", "review-fix.txt", "fixed")
	w.git(w.Other, "push", "-q", "origin", "synthetic-a")

	mustRun(t, "sync", "--apply")

	w.assertClean(w.Local)
	w.assertHas(w.Local, "synthetic-a", "review-fix.txt")
	w.assertHas(w.Local, "synthetic-a", "a.txt")
	// And with their commit here, publishing is a no-op rather than a refusal.
	stdout := mustRun(t, "push")
	if !strings.Contains(stdout, "up to date") {
		t.Errorf("after collecting the fix the branch is not level with the remote:\n%s", stdout)
	}
}

// extra-friendly-fixer: the reviewer rebases your branch as well as adding to
// it, so the published version shares no commit ids with yours.
//
// Nothing of yours is missing from it — that is what makes it yours still — so
// theirs supersedes. It is a reset rather than a fast-forward, which is why the
// plan names the two differently.
func TestJourneyAReviewerRebasesYourBranchAndPublishesIt(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "push", "--apply")

	// They add a fix and rewrite the whole branch, then publish it.
	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "switch", "-q", "-c", "synthetic-a", "origin/synthetic-a")
	w.commit(w.Other, "synthetic-a", "review-fix.txt", "fixed")
	w.git(w.Other, "rebase", "-q", "--force-rebase", "main")
	w.git(w.Other, "push", "-q", "--force", "origin", "synthetic-a")
	theirs := w.tip(w.Other, "synthetic-a")

	mustRun(t, "sync", "--apply")

	w.assertClean(w.Local)
	if now := w.tip(w.Local, "synthetic-a"); now != theirs {
		t.Errorf("local is at %s, want the published %s · their rebase did not survive", now, theirs)
	}
	w.assertHas(w.Local, "synthetic-a", "review-fix.txt")
	w.assertHas(w.Local, "synthetic-a", "a.txt")
}

// The refusal that separates the two: you have work the published version does
// not, so which one wins is not a decision to take behind your back.
func TestJourneyBothYouAndTheRemoteMovedYourBranch(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "push", "--apply")

	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "switch", "-q", "-c", "synthetic-a", "origin/synthetic-a")
	w.commit(w.Other, "synthetic-a", "theirs.txt", "theirs")
	w.git(w.Other, "push", "-q", "origin", "synthetic-a")

	w.commit(w.Local, "synthetic-a", "yours.txt", "yours")
	// Amend so neither side is an ancestor of the other and yours is not in
	// theirs by content either.
	w.git(w.Local, "commit", "-q", "--amend", "-m", "synthetic yours, revised")
	before := w.tip(w.Local, "synthetic-a")

	stdout, _, err := run(t, "sync", "--apply")
	if err == nil {
		t.Fatal("sync chose between two versions of your branch")
	}
	// Both counts, because "you have work the remote does not" is true of every
	// ordinary commit and a reader who just made one cannot tell them apart.
	for _, want := range []string{"both sides have moved", "not published", "not here"} {
		if !strings.Contains(stdout+err.Error(), want) {
			t.Errorf("the refusal does not say %q:\n%s\n%v", want, stdout, err)
		}
	}
	if after := w.tip(w.Local, "synthetic-a"); after != before {
		t.Errorf("a refused sync moved synthetic-a from %s to %s", before, after)
	}
	// The refusal names the way through rather than being a dead end.
	if !strings.Contains(stdout+err.Error(), "--take published") {
		t.Errorf("the refusal does not name the choice available:\n%s\n%v", stdout, err)
	}
	w.assertClean(w.Local)
}

// The commonest thing anybody does: commit on a published branch and sync.
//
// sync must ignore it. Local being ahead of its published version is
// unpublished work, which is push's business, and a refusal here would fire on
// every ordinary commit and make the command unusable. The refusal needs both
// sides to have moved, and this asserts the near miss.
func TestJourneyAnOrdinaryCommitDoesNotBlockSync(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "push", "--apply")

	w.commit(w.Local, "synthetic-a", "ordinary.txt", "ordinary")
	unpublished := w.tip(w.Local, "synthetic-a")

	stdout := mustRun(t, "sync")
	if strings.Contains(stdout, "diverged") || strings.Contains(stdout, "blocked") {
		t.Fatalf("an ordinary commit was treated as a divergence:\n%s", stdout)
	}

	mustRun(t, "sync", "--apply")
	if now := w.tip(w.Local, "synthetic-a"); now != unpublished {
		t.Errorf("sync moved a branch that was simply ahead: %s to %s", unpublished, now)
	}
	w.assertHas(w.Local, "synthetic-a", "ordinary.txt")
	w.assertClean(w.Local)

	// And again with the trunk having moved, which is the ordinary reason to
	// sync at all: still a replay, still no refusal.
	w.commit(w.Other, "main", "theirs.txt", "theirs")
	w.git(w.Other, "push", "-q", "origin", "main")

	mustRun(t, "sync", "--apply")
	if !w.contains(w.Local, "main", "synthetic-a") {
		t.Error("the stack was not replayed onto the advanced trunk")
	}
	w.assertHas(w.Local, "synthetic-a", "ordinary.txt")
	w.assertClean(w.Local)
}

// The same divergence, resolved by naming which side wins. This is the one path
// where sync loses work that exists nowhere else, so the preview lists every
// commit it would discard before anything happens.
func TestJourneyTakingThePublishedVersionOfADivergedBranch(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "push", "--apply")

	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "switch", "-q", "-c", "synthetic-a", "origin/synthetic-a")
	w.commit(w.Other, "synthetic-a", "theirs.txt", "theirs")
	w.git(w.Other, "push", "-q", "origin", "synthetic-a")
	theirs := w.tip(w.Other, "synthetic-a")

	w.commit(w.Local, "synthetic-a", "yours.txt", "yours")
	w.git(w.Local, "commit", "-q", "--amend", "-m", "synthetic yours, revised")

	preview := mustRun(t, "sync", "--take", "published")
	if !strings.Contains(preview, "discards") {
		t.Errorf("the preview does not say what it would lose:\n%s", preview)
	}

	mustRun(t, "sync", "--take", "published", "--apply")

	if now := w.tip(w.Local, "synthetic-a"); now != theirs {
		t.Errorf("synthetic-a is at %s, want the published %s", now, theirs)
	}
	w.assertHas(w.Local, "synthetic-a", "theirs.txt")
	w.assertClean(w.Local)
}

// An unknown value is refused before anything runs, and the refusal lists what
// the flag does take.
func TestJourneyAnUnknownTakeIsRefused(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")

	_, _, err := run(t, "sync", "--take", "synthetic-nonsense")
	if err == nil {
		t.Fatal("sync accepted --take synthetic-nonsense")
	}
	if !strings.Contains(err.Error(), "published") {
		t.Errorf("the refusal does not list what --take accepts: %v", err)
	}
}

// history reverter: you drop your last commit locally on purpose. It is already
// published, so the remote is now ahead of you.
//
// push refuses, which is the agreed behaviour — the preview has to be loud
// enough that you can choose the command that does what you meant.
func TestJourneyYouDropACommitYouAlreadyPublished(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.commit(w.Local, "synthetic-a", "regret.txt", "regret")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "push", "--apply")

	w.git(w.Local, "reset", "-q", "--hard", "HEAD~1")
	published := w.tip(w.Remote, "synthetic-a")

	stdout := mustRun(t, "push")

	if !strings.Contains(stdout, "remote is 1 commit ahead") {
		t.Errorf("preview does not say the remote is ahead:\n%s", stdout)
	}
	// Loud enough to act on: the command that does what you meant has to be in
	// the preview, because no g2g command does it.
	if !strings.Contains(stdout, "git push --force-with-lease") {
		t.Errorf("preview does not name the command that would republish:\n%s", stdout)
	}
	if _, _, err := run(t, "push", "--apply"); err == nil {
		t.Error("push rewound a published branch without being asked twice")
	}
	if now := w.tip(w.Remote, "synthetic-a"); now != published {
		t.Errorf("the remote moved from %s to %s", published, now)
	}
}

// Your remote branch was deleted after its pull request merged, and you still
// have it locally with no work of your own left.
//
// Recorded: push treats it as a branch the remote has never seen and would
// recreate it. We would rather it did not, unless local carries work that is
// not in the trunk.
func TestJourneyYourBranchWasDeletedAfterItMerged(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "push", "--apply")

	// It merges and the remote branch is deleted, as GitHub does on merge.
	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "switch", "-q", "main")
	w.git(w.Other, "merge", "-q", "--no-ff", "-m", "synthetic merge of a", "origin/synthetic-a")
	w.git(w.Other, "push", "-q", "origin", "main")
	w.git(w.Other, "push", "-q", "origin", "--delete", "synthetic-a")

	// You sync, which is what you would do next, and it must survive the branch
	// having gone from the remote: naming a deleted ref fails a whole fetch.
	mustRun(t, "sync", "--apply")
	stdout := mustRun(t, "push")

	// Absent from the remote has two meanings and they want opposite answers.
	// This branch is gone because it is finished, so putting it back is the
	// wrong reading.
	if strings.Contains(stdout, "new branch on the remote") {
		t.Errorf("push offered to recreate a branch that merged and was deleted:\n%s", stdout)
	}
	if !strings.Contains(stdout, "already in the trunk") {
		t.Errorf("push does not say the work has landed:\n%s", stdout)
	}
	// It reads as finished rather than broken, and the command that closes it
	// offers to.
	graph := mustRun(t, "graph")
	if strings.Contains(graph, "parent missing") {
		t.Errorf("a merged branch reads as broken:\n%s", graph)
	}
	if prune := mustRun(t, "prune"); !strings.Contains(prune, "synthetic-a") {
		t.Errorf("prune does not offer to forget the merged branch:\n%s", prune)
	}
	w.assertClean(w.Local)
}

// multi-user-conflict: the trunk moves upstream and your work collides with it.
//
// sync is fetch, advance, replay, and only the replay can conflict. The base
// still advances, because it was going to advance either way — so the recorded
// answer is a half-finished sync that says exactly where it stopped.
func TestJourneyTheAdvancedTrunkConflictsWithYourWork(t *testing.T) {
	w := newWorld(t)
	w.git(w.Local, "switch", "-qc", "synthetic-a")
	w.commit(w.Local, "synthetic-a", "shared.txt", "your version")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")

	w.commit(w.Other, "main", "shared.txt", "their version")
	w.git(w.Other, "push", "-q", "origin", "main")

	w.git(w.Local, "switch", "-q", "synthetic-a")
	stdout, _, _ := run(t, "sync", "--apply")

	if !strings.Contains(stdout, "stopped") {
		t.Fatalf("a conflicting sync did not say it stopped part-way:\n%s", stdout)
	}
	// The trunk advanced, which is what "half applied" means here and why the
	// message must not read as "nothing happened".
	if local, remote := w.tip(w.Local, "main"), w.tip(w.Other, "main"); local != remote {
		t.Errorf("the base was not advanced before the replay stopped: %s vs %s", local, remote)
	}
	mustRun(t, "restack", "--abort")
	w.assertClean(w.Local)
}

// self-conflict: you amend a branch low in the stack and the change collides
// with the branches above it.
//
// Recorded: restack refuses the whole thing when the selection forks, and names
// --scope path. A straight line has no fork, so it stops on the conflict and
// waits, which is the resumable engine doing its job.
func TestJourneyFixingALowBranchConflictsWithTheOnesAboveIt(t *testing.T) {
	w := newWorld(t)
	w.git(w.Local, "switch", "-qc", "synthetic-a")
	w.commit(w.Local, "synthetic-a", "shared.txt", "first")
	w.git(w.Local, "switch", "-qc", "synthetic-b")
	w.commit(w.Local, "synthetic-b", "shared.txt", "second")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a", "--apply")

	// Rewrite the lower branch so the upper one's edit no longer applies.
	w.git(w.Local, "switch", "-q", "synthetic-a")
	w.commit(w.Local, "synthetic-a", "shared.txt", "first, revised")
	w.git(w.Local, "commit", "-q", "--amend", "-m", "synthetic revised first")

	before := w.tip(w.Local, "synthetic-b")
	stdout, _, _ := run(t, "restack", "--scope", "stack", "--apply")

	if !strings.Contains(stdout, "conflict") && !strings.Contains(stdout, "stopped") {
		t.Fatalf("a cascading conflict was neither reported nor stopped on:\n%s", stdout)
	}
	if _, _, err := run(t, "restack", "--abort"); err == nil {
		if after := w.tip(w.Local, "synthetic-b"); after != before {
			t.Errorf("abort left synthetic-b at %s, want %s", after, before)
		}
	}
	w.assertClean(w.Local)
}

// borrower: somebody cherry-picked your commits into their branch and it landed
// first. Your commits are in the trunk under different object ids.
//
// This is what --no-reapply-cherry-picks is for: replaying must drop them by
// content rather than apply them twice.
func TestJourneySomeoneElseLandedYourCommitsFirst(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "borrowed.txt")
	w.commit(w.Local, "synthetic-a", "mine.txt", "mine")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "push", "--apply")

	// They take the first commit only, and land it on the trunk.
	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "switch", "-q", "main")
	borrowed := w.git(w.Local, "rev-parse", "synthetic-a~1")
	w.git(w.Other, "cherry-pick", borrowed)
	w.git(w.Other, "push", "-q", "origin", "main")

	w.git(w.Local, "switch", "-q", "synthetic-a")
	mustRun(t, "sync", "--apply")

	w.assertClean(w.Local)
	if !w.contains(w.Local, "main", "synthetic-a") {
		t.Fatal("synthetic-a was not brought onto the advanced trunk")
	}
	// One commit left of its own: the borrowed one is already in the trunk by
	// content, so replaying it would duplicate the change.
	if own := w.git(w.Local, "rev-list", "--count", "main..synthetic-a"); own != "1" {
		t.Errorf("synthetic-a has %s commits above the trunk, want 1: the borrowed commit was applied again", own)
	}
	w.assertHas(w.Local, "synthetic-a", "mine.txt")
}

// Out-of-order merge: the middle branch lands first, so the branch above it
// hangs from something that no longer exists and the one below is still open.
//
// Recorded. What we would want is for the branch below to be recognised as
// superseded by the merge and for the one above to land on the trunk.
func TestJourneyTheMiddleBranchOfYourStackMergesFirst(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	w.branchOff("synthetic-b", "synthetic-c", "c.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a", "--apply")
	mustRun(t, "track", "--branch", "synthetic-c", "--parent", "synthetic-b", "--apply")
	mustRun(t, "push", "--apply")

	// synthetic-b merges, which carries synthetic-a with it, and is deleted.
	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "switch", "-q", "main")
	w.git(w.Other, "merge", "-q", "--no-ff", "-m", "synthetic merge of b", "origin/synthetic-b")
	w.git(w.Other, "push", "-q", "origin", "main")
	w.git(w.Other, "push", "-q", "origin", "--delete", "synthetic-b")

	w.git(w.Local, "switch", "-q", "synthetic-c")
	stdout, _, err := run(t, "sync", "--apply")
	t.Logf("sync after an out-of-order merge: err=%v\n%s", err, stdout)

	w.assertClean(w.Local)
	if !w.contains(w.Local, "main", "synthetic-c") {
		t.Error("synthetic-c was not brought onto the merged trunk")
	}
	if own := w.git(w.Local, "rev-list", "--count", "main..synthetic-c"); own != "1" {
		t.Errorf("synthetic-c has %s commits above the trunk, want 1", own)
	}
	// The branches the merge carried must not read as broken. Telling someone
	// to retrack a branch that has already served its purpose sends them to
	// repair something that is finished.
	graph := mustRun(t, "graph", "--scope", "trunk")
	if strings.Contains(graph, "parent missing") {
		t.Errorf("a branch the merge carried reads as broken:\n%s", graph)
	}
	// And the command that closes them offers to.
	prune := mustRun(t, "prune", "--scope", "trunk")
	for _, branch := range []string{"synthetic-a", "synthetic-b"} {
		if !strings.Contains(prune, branch) {
			t.Errorf("prune does not offer to forget %s:\n%s", branch, prune)
		}
	}
}

// The world moves between preview and apply. Revalidation exists precisely for
// this, and it had never been tested against a remote that actually moved.
func TestJourneyTheRemoteMovesBetweenPreviewAndApply(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "push", "--apply")

	mustRun(t, "push")

	// Between the two invocations, somebody publishes.
	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "switch", "-q", "-c", "synthetic-a", "origin/synthetic-a")
	w.commit(w.Other, "synthetic-a", "theirs.txt", "theirs")
	w.git(w.Other, "push", "-q", "origin", "synthetic-a")
	theirs := w.tip(w.Other, "synthetic-a")

	w.commit(w.Local, "synthetic-a", "yours.txt", "yours")
	if _, _, err := run(t, "push", "--apply"); err == nil {
		t.Fatal("push applied a plan the world had moved under")
	}
	if now := w.tip(w.Remote, "synthetic-a"); now != theirs {
		t.Errorf("the remote branch changed from %s to %s", theirs, now)
	}
}

// Atomicity: push claims every selected ref advances together or none does.
// One branch's lease failing has to leave the other exactly where it was.
func TestJourneyOneRejectedBranchStopsTheWholePush(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a", "--apply")
	mustRun(t, "push", "--apply")

	// Only the lower branch moves under us.
	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "switch", "-q", "-c", "synthetic-a", "origin/synthetic-a")
	w.commit(w.Other, "synthetic-a", "theirs.txt", "theirs")
	w.git(w.Other, "push", "-q", "origin", "synthetic-a")

	// And we add work to the upper one, which on its own would push cleanly.
	w.commit(w.Local, "synthetic-b", "more.txt", "more")
	untouched := w.tip(w.Remote, "synthetic-b")

	if _, _, err := run(t, "push", "--apply"); err == nil {
		t.Fatal("push proceeded with one branch the remote had moved")
	}
	if now := w.tip(w.Remote, "synthetic-b"); now != untouched {
		t.Errorf("synthetic-b advanced to %s despite the push being refused, want %s", now, untouched)
	}
}

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

// A branch another worktree has checked out is refused, because a rewrite moves
// a ref without checking anything out and would strand that worktree.
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

	stdout, _, err := run(t, "restack", "--scope", "stack", "--apply")
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

// sync moves contents, never structure. It replays onto a ref it fetched under
// refs/g2g/, because that is where the trunk is about to be — and recording
// that as the parent left every synced stack hanging from an internal ref:
//
//	○ refs/g2g/remotes/origin/main  trunk
//	● synthetic-a                   parent missing
//	Recorded parent is no longer a local branch for synthetic-a · retrack.
//
// on the ordinary happy path, immediately after a sync that reported success.
func TestJourneySyncLeavesTheRecordedStructureAlone(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a", "--apply")

	w.commit(w.Other, "main", "colleague.txt", "theirs")
	w.git(w.Other, "push", "-q", "origin", "main")
	w.git(w.Local, "switch", "-q", "synthetic-b")

	before := w.readStructure()
	mustRun(t, "sync", "--apply")

	// Fork points move, because a replay changes where each branch forks. What
	// must not move is the structure: who hangs from whom, and what the trunks
	// are.
	if after := w.readStructure(); !maps.Equal(after, before) {
		t.Errorf("sync changed the recorded structure:\nbefore: %v\nafter:  %v", before, after)
	}
	// And the graph reads as a healthy stack rather than a broken one.
	graph := mustRun(t, "graph", "--scope", "trunk")
	if strings.Contains(graph, "refs/g2g/") {
		t.Errorf("an internal ref reached the rendered graph:\n%s", graph)
	}
	if strings.Contains(graph, "parent missing") {
		t.Errorf("a freshly synced stack reads as broken:\n%s", graph)
	}
}

// An explicit --onto is the opposite case: the user is asking for the branch to
// move, so the graph must record it. Separating the two meanings must not have
// cost the one that is a real reparent.
func TestJourneyAnExplicitOntoStillRecordsTheNewParent(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-base", "base-work.txt")
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-base", "--parent", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")

	w.git(w.Local, "switch", "-q", "synthetic-a")
	mustRun(t, "restack", "--onto", "synthetic-base", "--apply")

	if !strings.Contains(w.readStore(), `"parent": "synthetic-base"`) {
		t.Errorf("an explicit --onto did not record the new parent:\n%s", w.readStore())
	}
	if !w.contains(w.Local, "synthetic-base", "synthetic-a") {
		t.Error("synthetic-a was not replayed onto its new parent")
	}
	w.assertClean(w.Local)
}

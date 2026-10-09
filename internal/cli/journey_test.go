package cli_test

// These are journeys, not command checks: a person works on a stack while the
// remote moves under them, and each one runs to the point where they are either
// finished or told what to do next.
//
// Everything here is real except GitHub. Real Git, a real bare remote, and a
// real second clone standing in for a colleague. Every mutation is followed by
// a clean-tree assertion, because the failure that keeps recurring is a ref
// moving without the working tree following it.

import (
	"maps"
	"strings"
	"testing"
)

// A branch made from the remote trunk must not replay that trunk's commits
// just because the local trunk was behind when its edge was recorded.
func TestJourneyTrackingWhileLocalTrunkLagsThenPulling(t *testing.T) {
	for _, known := range []bool{true, false} {
		t.Run(map[bool]string{true: "known-upstream", false: "upstream-not-fetched"}[known], func(t *testing.T) {
			w := newWorld(t)
			stale := w.tip(w.Local, "main")
			w.commit(w.Other, "main", "trunk.txt", "synthetic upstream first")
			w.commit(w.Other, "main", "trunk.txt", "synthetic upstream second")
			w.git(w.Other, "push", "-q", "origin", "main")
			w.git(w.Local, "fetch", "-q", "origin")
			fork := w.tip(w.Local, "origin/main")
			w.git(w.Local, "switch", "-qc", "synthetic-a", "origin/main")
			w.commit(w.Local, "synthetic-a", "a.txt", "synthetic feature work")
			if !known {
				// The commits are here but the upstream ref no longer provides
				// evidence. Pull must also repair an older recorded boundary.
				w.git(w.Local, "update-ref", "refs/remotes/origin/main", stale)
			}
			mustRun(t, "track", "--parent", "main", "--apply")
			w.assertClean(w.Local)
			want := stale
			if known {
				want = fork
			}
			if got := w.tip(w.Local, "refs/g2g/forkpoints/synthetic-a"); got != want {
				t.Fatalf("fork = %s, want %s", got, want)
			}
			w.commit(w.Other, "main", "trunk.txt", "synthetic upstream latest")
			w.git(w.Other, "push", "-q", "origin", "main")
			remote := w.tip(w.Remote, "main")
			mustRun(t, "pull")
			w.assertClean(w.Local)
			if w.tip(w.Local, "main") != stale {
				t.Fatal("preview advanced trunk")
			}
			mustRun(t, "pull", "--apply")
			w.assertClean(w.Local)
			if w.tip(w.Local, "main") != remote || !w.contains(w.Local, "main", "synthetic-a") {
				t.Fatal("pull did not place feature on remote trunk")
			}
			if got := w.git(w.Local, "rev-list", "--count", "main..synthetic-a"); got != "1" {
				t.Fatalf("feature has %s commits, want only its own", got)
			}
			w.assertHas(w.Local, "synthetic-a", "a.txt")
			mustRun(t, "pull", "--apply")
			w.assertClean(w.Local)
		})
	}
}

// Tracking against a trunk that advanced must produce a usable replay range.
func TestJourneyTrackingAnOldBranchOnAnAdvancedTrunkThenPulling(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	w.commit(w.Other, "main", "advanced.txt", "synthetic trunk work")
	w.git(w.Other, "push", "-q", "origin", "main")
	w.git(w.Local, "switch", "main")
	w.git(w.Local, "pull", "--ff-only", "origin", "main")
	w.git(w.Local, "switch", "synthetic-b")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	w.assertClean(w.Local)
	mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a", "--apply")
	w.assertClean(w.Local)
	mustRun(t, "pull", "--apply")
	w.assertClean(w.Local)
	for _, branch := range []string{"synthetic-a", "synthetic-b"} {
		if !w.contains(w.Local, "main", branch) {
			t.Errorf("%s not replayed onto advanced trunk", branch)
		}
		w.assertHas(w.Local, branch, "a.txt")
	}
	w.assertHas(w.Local, "synthetic-b", "b.txt")
}

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
	mustRun(t, "pull", "--apply")

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
	stdout := mustRun(t, "pull", "--apply")

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
	_, _, err := run(t, "pull", "--apply")

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
	if !strings.Contains(stdout+err.Error(), "the published version differs") {
		t.Errorf("refusal does not describe the published difference:\n%s\n%v", stdout, err)
	}
	if now := w.tip(w.Remote, "synthetic-a"); now != theirs {
		t.Errorf("the remote branch changed from %s to %s", theirs, now)
	}
}

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

	mustRun(t, "pull", "--apply")

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

	mustRun(t, "pull", "--apply")

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

	stdout, _, err := run(t, "pull", "--apply")
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

	stdout := mustRun(t, "pull")
	if strings.Contains(stdout, "diverged") || strings.Contains(stdout, "blocked") {
		t.Fatalf("an ordinary commit was treated as a divergence:\n%s", stdout)
	}

	mustRun(t, "pull", "--apply")
	if now := w.tip(w.Local, "synthetic-a"); now != unpublished {
		t.Errorf("sync moved a branch that was simply ahead: %s to %s", unpublished, now)
	}
	w.assertHas(w.Local, "synthetic-a", "ordinary.txt")
	w.assertClean(w.Local)

	// And again with the trunk having moved, which is the ordinary reason to
	// sync at all: still a replay, still no refusal.
	w.commit(w.Other, "main", "theirs.txt", "theirs")
	w.git(w.Other, "push", "-q", "origin", "main")

	mustRun(t, "pull", "--apply")
	if !w.contains(w.Local, "main", "synthetic-a") {
		t.Error("the stack was not replayed onto the advanced trunk")
	}
	w.assertHas(w.Local, "synthetic-a", "ordinary.txt")
	w.assertClean(w.Local)
}

// A replay you have not published yet is yours, not a divergence.
//
// After a sync replays the stack onto a trunk that moved, every branch is ahead
// of its published version by content and behind it by commit id. Counted by
// id, the trunk's new commits read as work here that is not published and the
// branch as moved on both sides, so the second sync of the day refused a stack
// nobody else had touched.
func TestJourneySyncingAgainBeforePublishingTheReplay(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a", "--apply")
	mustRun(t, "push", "--apply")
	published := w.tip(w.Remote, "synthetic-b")

	w.commit(w.Other, "main", "first.txt", "first")
	w.git(w.Other, "push", "-q", "origin", "main")
	w.git(w.Local, "switch", "-q", "synthetic-b")
	mustRun(t, "pull", "--apply")

	w.commit(w.Other, "main", "second.txt", "second")
	w.git(w.Other, "push", "-q", "origin", "main")
	mustRun(t, "pull", "--apply")

	w.assertClean(w.Local)
	for _, branch := range []string{"synthetic-a", "synthetic-b"} {
		if !w.contains(w.Local, "main", branch) {
			t.Errorf("%s was not replayed onto the trunk the second time", branch)
		}
	}
	w.assertHas(w.Local, "synthetic-b", "second.txt")
	w.assertHas(w.Local, "synthetic-b", "b.txt")
	if now := w.tip(w.Remote, "synthetic-b"); now != published {
		t.Errorf("sync published synthetic-b: the remote moved from %s to %s", published, now)
	}
}

// You committed on the trunk and have not pushed it. That is the trunk being
// ahead, not diverged, and the stack still syncs.
//
// It was refused as both sides having moved, with --take published offered as
// the way through -- which would have discarded the commit it was ahead by.
func TestJourneyATrunkAheadOfItsRemoteIsNotDiverged(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	w.commit(w.Local, "main", "local-trunk.txt", "local")
	ahead := w.tip(w.Local, "main")
	w.git(w.Local, "switch", "-q", "synthetic-a")

	mustRun(t, "pull", "--apply")

	if now := w.tip(w.Local, "main"); now != ahead {
		t.Errorf("sync moved the trunk from %s to %s", ahead, now)
	}
	if !w.contains(w.Local, "main", "synthetic-a") {
		t.Error("synthetic-a was not replayed onto the trunk it is recorded on")
	}
	w.assertClean(w.Local)
}

// A branch the remote deleted is not published, whatever g2g fetched of it
// before.
//
// Nothing prunes refs/g2g/remotes/, so the version fetched by an earlier sync
// is still there after the remote lets the branch go. Reading it as the
// published version fast-forwarded a branch back onto the commit its owner had
// just dropped.
func TestJourneyABranchTheRemoteDeletedIsNotPublished(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.commit(w.Local, "synthetic-a", "regret.txt", "regret")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "push", "--apply")
	mustRun(t, "pull", "--apply")

	w.git(w.Other, "push", "-q", "origin", "--delete", "synthetic-a")
	w.git(w.Local, "reset", "-q", "--hard", "HEAD~1")
	dropped := w.tip(w.Local, "synthetic-a")

	mustRun(t, "pull", "--apply")

	if now := w.tip(w.Local, "synthetic-a"); now != dropped {
		t.Errorf("sync moved synthetic-a from %s to %s · it brought back the commit that was dropped", dropped, now)
	}
	w.assertClean(w.Local)
}

// extra-friendly-fixer, with a branch stacked on top of the one they fixed.
//
// collect moves your branch to the published version, and the replay was
// planned before that happened — against the tip the branch had when the plan
// was made, which by then is a commit it no longer points at. So the child was
// measured against where its parent used to be, judged to need no replay, and
// left dangling there while the run reported "Synced."
//
// No --take involved: the published version is content-equal, so it supersedes
// on its own. Every collect test was one branch deep, which is why this stood.
func TestJourneyAChildOfASupersededBranchIsReplayedOntoIt(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a", "--apply")
	mustRun(t, "push", "--apply")

	// They rebase synthetic-a and publish it: same content, new ids.
	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "switch", "-q", "-c", "synthetic-a", "origin/synthetic-a")
	w.git(w.Other, "commit", "-q", "--amend", "-m", "synthetic a, reworded")
	w.git(w.Other, "push", "-q", "--force", "origin", "synthetic-a")
	theirs := w.tip(w.Other, "synthetic-a")

	w.git(w.Local, "switch", "-q", "synthetic-b")
	mustRun(t, "pull", "--apply")

	if now := w.tip(w.Local, "synthetic-a"); now != theirs {
		t.Errorf("synthetic-a is at %s, want the published %s", now, theirs)
	}
	if !w.contains(w.Local, "synthetic-a", "synthetic-b") {
		t.Error("synthetic-b was stranded on the commit synthetic-a no longer points at")
	}
	w.assertHas(w.Local, "synthetic-b", "b.txt")
	w.assertClean(w.Local)
}

// history reverter: you drop your last commit locally on purpose. It is already
// published, so the remote is now ahead of you.
//
// Your push published it, so the sync point says you had the commit: the
// remote being ahead by it is your drop, not somebody else's work. push
// publishes the drop, naming the commit, rather than refusing and leaving you
// to reach for git push --force-with-lease.
func TestJourneyYouDropACommitYouAlreadyPublished(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.commit(w.Local, "synthetic-a", "regret.txt", "regret")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "push", "--apply")
	regret := w.tip(w.Local, "synthetic-a")

	w.git(w.Local, "reset", "-q", "--hard", "HEAD~1")

	stdout := mustRun(t, "push")
	for _, want := range []string{"drops 1 published commit you dropped here", "synthetic-a " + regret[:12] + " synthetic regret.txt"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("preview does not say %q:\n%s", want, stdout)
		}
	}
	mustRun(t, "push", "--apply")
	if now, want := w.tip(w.Remote, "synthetic-a"), w.tip(w.Local, "synthetic-a"); now != want {
		t.Errorf("the remote holds %s, want the branch without the dropped commit, %s", now, want)
	}

	// Without a sync point -- a branch published with plain git -- nothing says
	// whose the remote's extra commit is, and push refuses as it always did,
	// naming the command that does what a person who meant it wants.
	w.git(w.Local, "update-ref", "-d", "refs/g2g/synced/origin/synthetic-a")
	w.commit(w.Local, "synthetic-a", "again.txt", "again")
	w.git(w.Local, "push", "-q", "origin", "synthetic-a")
	w.git(w.Local, "reset", "-q", "--hard", "HEAD~1")
	published := w.tip(w.Remote, "synthetic-a")
	stdout = mustRun(t, "push")
	if !strings.Contains(stdout, "remote has 1 commit this does not · publishing would drop it") ||
		!strings.Contains(stdout, "git push --atomic --force-with-lease=refs/heads/synthetic-a:"+published) {
		t.Errorf("preview without a sync point does not refuse and name the replacement:\n%s", stdout)
	}
	if _, _, err := run(t, "push", "--apply"); err == nil {
		t.Error("push rewound a published branch it has no sync point for")
	}
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
	mustRun(t, "pull", "--apply")

	// Fork points move, because a replay changes where each branch forks. What
	// must not move is the structure: who hangs from whom, and what the trunks
	// are.
	if after := w.readStructure(); !maps.Equal(after, before) {
		t.Errorf("sync changed the recorded structure:\nbefore: %v\nafter:  %v", before, after)
	}
	// And the graph reads as a healthy stack rather than a broken one.
	graph := mustRun(t, "status", "--scope", "trunk")
	if strings.Contains(graph, "refs/g2g/") {
		t.Errorf("an internal ref reached the rendered graph:\n%s", graph)
	}
	if strings.Contains(graph, "parent missing") {
		t.Errorf("a freshly synced stack reads as broken:\n%s", graph)
	}
}

// A reviewer pushed to the branch while it was restacked here onto a trunk that
// had moved. Counted without a bound, every commit the trunk gained read as
// this branch's own unpublished work, and the refusal claimed a divergence the
// branch did not have. Everything of its own is on the remote by content, so
// what is left to say is the true reason it stops: the published version sits
// on an older trunk than the one recorded, and which of its commits are its
// own cannot be read from ancestry — a parent that was amended looks the same.
func TestJourneyPullAfterALocalRestackRefusesForTheTrueReason(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "push", "--apply")

	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "switch", "-q", "-c", "synthetic-a", "origin/synthetic-a")
	w.commit(w.Other, "synthetic-a", "review.txt", "review")
	w.git(w.Other, "push", "-q", "origin", "synthetic-a")
	for _, name := range []string{"one.txt", "two.txt", "three.txt"} {
		w.commit(w.Other, "main", name, name)
	}
	w.git(w.Other, "push", "-q", "origin", "main")

	w.git(w.Local, "switch", "-q", "main")
	w.git(w.Local, "pull", "-q", "--ff-only", "origin", "main")
	w.git(w.Local, "switch", "-q", "synthetic-a")
	mustRun(t, "restack", "--apply")
	before := w.tip(w.Local, "synthetic-a")

	out, _, err := run(t, "pull", "--apply")
	if err == nil {
		t.Fatalf("pull took a version whose own commits cannot be told:\n%s", out)
	}
	if strings.Contains(out, "here that are not published") {
		t.Errorf("the refusal counts the trunk's commits as this branch's own:\n%s", out)
	}
	if !strings.Contains(out, "which commits are its own cannot be told") {
		t.Errorf("the refusal does not say why:\n%s", out)
	}
	if after := w.tip(w.Local, "synthetic-a"); after != before {
		t.Errorf("a refused pull moved synthetic-a from %s to %s", before, after)
	}
	w.assertClean(w.Local)
}

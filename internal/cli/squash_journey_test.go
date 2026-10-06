package cli_test

import (
	"maps"
	"strings"
	"testing"
)

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
	mustRun(t, "pull", "--apply")

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
	mustRun(t, "pull", "--apply")
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
	graph := mustRun(t, "status")
	if strings.Contains(graph, "parent missing") {
		t.Errorf("a merged branch reads as broken:\n%s", graph)
	}
	if prune := mustRun(t, "prune"); !strings.Contains(prune, "synthetic-a") {
		t.Errorf("prune does not offer to forget the merged branch:\n%s", prune)
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
	mustRun(t, "pull", "--apply")

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
	stdout, _, err := run(t, "pull", "--apply")
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
	graph := mustRun(t, "status", "--scope", "trunk")
	if strings.Contains(graph, "parent missing") {
		t.Errorf("a branch the merge carried reads as broken:\n%s", graph)
	}
	// They read as having nothing of their own, which is what is actually
	// knowable: whether a branch sitting on the trunk with no commits is
	// finished or not yet started is not something the recorded state says.
	if !strings.Contains(graph, "no commits of its own") {
		t.Errorf("the branches the merge carried are unremarked:\n%s", graph)
	}
	// And the command that closes them offers to.
	prune := mustRun(t, "prune", "--scope", "trunk")
	for _, branch := range []string{"synthetic-a", "synthetic-b"} {
		if !strings.Contains(prune, branch) {
			t.Errorf("prune does not offer to forget %s:\n%s", branch, prune)
		}
	}
}

// Your parent was squash-merged, and it had more than one commit.
//
// This is the commonest way a branch lands and the case per-commit detection
// cannot see: a squash combines the commits into one, so that commit is
// equivalent to none of them and each is offered to the rewrite engine
// individually — where it conflicts with the squashed version of itself. Found
// by landing this repository's own stack.
func TestJourneyYourParentWasSquashMergedWithSeveralCommits(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "first.txt")
	w.commit(w.Local, "synthetic-a", "second.txt", "second")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a", "--apply")
	mustRun(t, "push", "--apply")

	// Squashed on the remote, then the trunk moves on, so nothing as crude as
	// a tree comparison would see it either.
	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "switch", "-q", "main")
	w.git(w.Other, "merge", "-q", "--squash", "origin/synthetic-a")
	w.git(w.Other, "commit", "-qm", "synthetic squash of a")
	w.commit(w.Other, "main", "elsewhere.txt", "elsewhere")
	w.git(w.Other, "push", "-q", "origin", "main")

	w.git(w.Local, "switch", "-q", "synthetic-b")
	mustRun(t, "pull", "--apply")

	w.assertClean(w.Local)
	if !w.contains(w.Local, "main", "synthetic-b") {
		t.Fatal("synthetic-b was not brought onto the squashed trunk")
	}
	// Its own work and nothing else: the parent's two commits are in the trunk
	// as one, and replaying them again is what used to conflict.
	if own := w.git(w.Local, "rev-list", "--count", "main..synthetic-b"); own != "1" {
		t.Errorf("synthetic-b has %s commits above the trunk, want 1", own)
	}
	w.assertHas(w.Local, "synthetic-b", "b.txt")
	w.assertHas(w.Local, "synthetic-b", "first.txt")
	w.assertHas(w.Local, "synthetic-b", "second.txt")

	// And the parent reads as finished rather than as needing repair.
	graph := mustRun(t, "status", "--scope", "trunk")
	if strings.Contains(graph, "needs restack") {
		t.Errorf("a squash-merged parent still reads as needing a restack:\n%s", graph)
	}
}

// squashedParent is main ← a ← b, published, with a then squash-merged into
// main by a colleague: the commonest way a branch lands.
func squashedParent(t *testing.T) *world {
	t.Helper()
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "first.txt")
	w.commit(w.Local, "synthetic-a", "second.txt", "second")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a", "--apply")
	mustRun(t, "push", "--apply")

	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "switch", "-q", "main")
	w.git(w.Other, "merge", "-q", "--squash", "origin/synthetic-a")
	w.git(w.Other, "commit", "-qm", "synthetic squash of a")
	w.git(w.Other, "push", "-q", "origin", "main")
	w.git(w.Local, "switch", "-q", "synthetic-b")
	return w
}

// assertLanded checks the end state both ways of forgetting a squashed parent
// reach: the parent forgotten, the child recorded on the trunk with only its
// own work above it, and nothing left in the working tree.
func (w *world) assertLanded(t *testing.T) {
	t.Helper()
	if structure := w.readStructure(); structure["synthetic-a"] != "" || structure["synthetic-b"] != "main" {
		t.Errorf("recorded structure = %v, want synthetic-a forgotten and synthetic-b on main", structure)
	}
	if own := w.git(w.Local, "rev-list", "--count", "main..synthetic-b"); own != "1" {
		t.Errorf("synthetic-b has %s commits above the trunk, want its own one", own)
	}
	w.assertHas(w.Local, "synthetic-b", "b.txt")
	// The rehomed edge's fork point is pinned, or it can be collected.
	if pin := w.git(w.Local, "rev-parse", "refs/g2g/forkpoints/synthetic-b"); pin != w.tip(w.Local, "main") {
		t.Errorf("synthetic-b's fork point is pinned at %s, want the trunk's tip", pin)
	}
	w.assertClean(w.Local)
}

// The trunk was brought up to date by hand, so the parent reads as landed —
// but the child still carries the parent's original commits, nothing says it
// belongs on the trunk, and prune refuses to strand it. The way out it names
// has to be one that works.
//
// It once offered widening the selection, which brings the child in to be
// asked about and finds it has work of its own, so it refuses again: a circle,
// on the commonest way a branch lands.
func TestJourneyPullPruneForgetsASquashedParent(t *testing.T) {
	w := squashedParent(t)
	w.git(w.Local, "switch", "-q", "main")
	w.git(w.Local, "pull", "-q", "--ff-only", "origin", "main")
	w.git(w.Local, "switch", "-q", "synthetic-b")

	refused := mustRun(t, "prune", "--scope", "trunk")
	if !strings.Contains(refused, "g2g pull --prune") {
		t.Fatalf("prune does not name pull --prune as the way out:\n%s", refused)
	}
	if strings.Contains(refused, "widen the selection") {
		t.Errorf("prune offers widening a selection that already holds the child:\n%s", refused)
	}

	pulled := mustRun(t, "pull", "--prune", "--apply")
	for _, want := range []string{"Pulled.", "Records synthetic-b on main", "Forgotten."} {
		if !strings.Contains(pulled, want) {
			t.Errorf("pull --prune does not report %q:\n%s", want, pulled)
		}
	}
	w.assertLanded(t)
}

// After a plain pull the child already sits on the trunk, so prune records it
// there itself rather than refusing and sending the reader to track.
func TestJourneyPruneAfterAPullRecordsTheChildWhereItSits(t *testing.T) {
	w := squashedParent(t)
	pulled := mustRun(t, "pull", "--apply")
	// The pull found the parent landed, so forgetting it comes before
	// publishing anything; pushing first would publish a branch that has
	// landed.
	if !strings.Contains(pulled, "Suggested next step: g2g prune\n") {
		t.Errorf("pull does not suggest forgetting what landed:\n%s", pulled)
	}

	preview := mustRun(t, "prune", "--scope", "trunk")
	if !strings.Contains(preview, "Records synthetic-b on main, where it already sits.") {
		t.Fatalf("prune does not say where it records the child:\n%s", preview)
	}
	mustRun(t, "prune", "--scope", "trunk", "--apply")
	w.assertLanded(t)
}

// The prune a pull suggests is the command as offered, with no flags added: it
// has to select what the pull found landed.
func TestJourneyThePruneAPullSuggestsForgetsWhatLanded(t *testing.T) {
	w := squashedParent(t)
	mustRun(t, "pull", "--apply")
	mustRun(t, "prune", "--apply")
	w.assertLanded(t)
}

// A preview of pull --prune changes nothing: it cannot show the prune, because
// what has landed is only known once the base moves, so it says it will do one.
// Machine output is one document and this is two reports, so it is refused.
func TestJourneyPullPrunePreviewsWithoutForgetting(t *testing.T) {
	w := squashedParent(t)
	before := w.readStructure()

	preview := mustRun(t, "pull", "--prune")
	if !strings.Contains(preview, "then forget what has landed") {
		t.Errorf("the preview does not say it will prune:\n%s", preview)
	}
	if _, _, err := run(t, "pull", "--prune", "--json"); err == nil || !strings.Contains(err.Error(), "one document") {
		t.Errorf("pull --prune --json error = %v, want it refused", err)
	}
	if after := w.readStructure(); !maps.Equal(before, after) {
		t.Errorf("recorded structure changed from %v to %v", before, after)
	}
	w.assertClean(w.Local)
}

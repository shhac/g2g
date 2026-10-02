package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	stdout, _, _ := run(t, "pull", "--apply")

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

// The same squash, restacked directly rather than through sync.
//
// sync replays onto a fetched ref and takes a different path, so it passes
// whether or not a squashed parent collapses. This is the path that failed:
// the parent stays in the replay range, its commits are offered to the engine
// one at a time, and each conflicts with the squashed version of itself.
func TestJourneyRestackingOntoASquashedParentDirectly(t *testing.T) {
	w := newWorld(t)
	// Successive edits to one file, which is what a real branch looks like and
	// what makes this fail: replaying the first onto a trunk that already holds
	// the combined result conflicts, where two commits touching different files
	// would each replay as a no-op and be dropped as empty.
	w.branchOff("main", "synthetic-a", "work.txt")
	w.commit(w.Local, "synthetic-a", "work.txt", "work\nand more work")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a", "--apply")

	// Squashed locally, as landing a pull request and pulling would leave it.
	w.git(w.Local, "switch", "-q", "main")
	w.git(w.Local, "merge", "-q", "--squash", "synthetic-a")
	w.git(w.Local, "commit", "-qm", "synthetic squash of a")
	w.commit(w.Local, "main", "elsewhere.txt", "elsewhere")

	w.git(w.Local, "switch", "-q", "synthetic-b")
	mustRun(t, "restack", "--scope", "stack", "--apply")

	w.assertClean(w.Local)
	if own := w.git(w.Local, "rev-list", "--count", "main..synthetic-b"); own != "1" {
		t.Errorf("synthetic-b has %s commits above the trunk, want 1 · the squashed parent was replayed", own)
	}
	w.assertHas(w.Local, "synthetic-b", "b.txt")
	w.assertHas(w.Local, "synthetic-b", "work.txt")
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

// The same request when the branch already sits on its new parent, which is
// where a landed parent leaves its child. Nothing needs replaying, and the
// command used to treat "no steps" as "nothing to do": it said so and left the
// branch recorded under the parent it had just been moved off.
func TestJourneyAnOntoWithNothingToReplayStillRecordsTheNewParent(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a", "--apply")
	// synthetic-a lands by fast-forward, so synthetic-b already sits on main.
	w.git(w.Local, "switch", "-q", "main")
	w.git(w.Local, "merge", "-q", "--ff-only", "synthetic-a")

	mustRun(t, "restack", "--branch", "synthetic-b", "--onto", "main", "--apply")

	if !strings.Contains(w.readStore(), `"synthetic-b": {
      "parent": "main"`) {
		t.Errorf("--onto did not record the new parent when there was nothing to replay:\n%s", w.readStore())
	}
	w.assertClean(w.Local)
}

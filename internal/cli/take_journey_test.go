package cli_test

import (
	"strings"
	"testing"
)

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

	preview := mustRun(t, "pull", "--take", "published")
	if !strings.Contains(preview, "discards") {
		t.Errorf("the preview does not say what it would lose:\n%s", preview)
	}

	mustRun(t, "pull", "--take", "published", "--apply")

	if now := w.tip(w.Local, "synthetic-a"); now != theirs {
		t.Errorf("synthetic-a is at %s, want the published %s", now, theirs)
	}
	w.assertHas(w.Local, "synthetic-a", "theirs.txt")
	w.assertClean(w.Local)
}

// The trunk itself, both sides moved. This is the most destructive thing the
// tool does — it hard-resets the branch everything else is built on, dropping
// commits that exist nowhere else — and nothing exercised it, so the preview
// had gone contradictory unnoticed: it claimed the published trunk "already
// has everything here" while listing the commits it was about to lose.
func TestJourneyTakingThePublishedVersionOfADivergedTrunk(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")

	// A colleague rewrites the trunk and force-pushes it.
	w.git(w.Other, "switch", "-q", "main")
	w.commit(w.Other, "main", "theirs.txt", "theirs")
	w.git(w.Other, "push", "-q", "--force", "origin", "main")
	theirs := w.tip(w.Other, "main")

	// And you have a commit on your trunk that never left this machine, so the
	// two have genuinely diverged rather than one being behind.
	w.git(w.Local, "switch", "-q", "main")
	w.commit(w.Local, "main", "mine.txt", "mine")
	mine := w.tip(w.Local, "main")
	w.git(w.Local, "switch", "-q", "synthetic-a")

	// Refused by default: choosing between two versions of the trunk is not
	// something to do behind somebody's back.
	refused, _, _ := run(t, "pull")
	if !strings.Contains(refused, "both sides have moved on main") {
		t.Errorf("a diverged trunk was not refused:\n%s", refused)
	}
	if now := w.tip(w.Local, "main"); now != mine {
		t.Errorf("the refused sync moved the trunk to %s", now)
	}

	preview := mustRun(t, "pull", "--take", "published")
	// Every commit it would lose, by name.
	if !strings.Contains(preview, "discards") || !strings.Contains(preview, mine[:7]) {
		t.Errorf("the preview does not name the trunk commit it would lose:\n%s", preview)
	}
	// And it must not claim the published trunk already has everything here,
	// which is the opposite of true when it is about to discard something.
	if strings.Contains(preview, "already has everything here") {
		t.Errorf("the preview contradicts itself — it discards and claims to lose nothing:\n%s", preview)
	}

	mustRun(t, "pull", "--take", "published", "--apply")

	if now := w.tip(w.Local, "main"); now != theirs {
		t.Errorf("main is at %s, want the published %s", now, theirs)
	}
	w.assertHas(w.Local, "main", "theirs.txt")
	// The stack above it was replayed onto the taken trunk rather than stranded.
	if !w.contains(w.Local, "main", "synthetic-a") {
		t.Error("synthetic-a was not replayed onto the taken trunk")
	}
	w.assertHas(w.Local, "synthetic-a", "a.txt")
	w.assertClean(w.Local)
}

// The boundary is a narrowing, not an enabler: it stops --take published
// reaching branches you were not thinking about.
//
// --take published is otherwise all or nothing, and it only ever changes the
// outcome for a branch that has genuinely diverged from its own published
// version — that is the single case collect consults it for. So with two
// diverged branches it takes both, discarding local work on the upper one
// alongside the lower one you actually meant. --through stops at the branch
// you named and refuses the rest, which is the whole point: a boundary says
// where you have decided.
func TestJourneyABoundaryRefusesTheDivergenceAboveIt(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a", "--apply")
	mustRun(t, "push", "--apply")

	// Somebody publishes their own version of both branches.
	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "switch", "-q", "-c", "synthetic-a", "origin/synthetic-a")
	w.commit(w.Other, "synthetic-a", "theirs-a.txt", "theirs")
	w.git(w.Other, "switch", "-q", "-c", "synthetic-b", "origin/synthetic-b")
	w.commit(w.Other, "synthetic-b", "theirs-b.txt", "theirs")
	w.git(w.Other, "push", "-q", "--force", "origin", "synthetic-a", "synthetic-b")

	// And you have your own on both, so both have genuinely diverged.
	w.commit(w.Local, "synthetic-a", "mine-a.txt", "mine")
	w.commit(w.Local, "synthetic-b", "mine-b.txt", "mine")
	w.git(w.Local, "switch", "-q", "synthetic-b")
	before := w.tip(w.Local, "synthetic-b")

	// Bounded at synthetic-a: the branch above it still needs a decision, and
	// nothing is touched until one is made.
	bounded, _, _ := run(t, "pull", "--take", "published", "--through", "synthetic-a", "--apply")
	if !strings.Contains(bounded, "synthetic-b") {
		t.Errorf("the refusal does not name the branch above the boundary:\n%s", bounded)
	}
	if now := w.tip(w.Local, "synthetic-b"); now != before {
		t.Errorf("synthetic-b moved to %s despite the run being refused", now)
	}

	// Unbounded, the same command takes synthetic-b as well — which is exactly
	// what the boundary is there to prevent.
	mustRun(t, "pull", "--take", "published", "--apply")
	w.assertHas(w.Local, "synthetic-b", "theirs-b.txt")
	w.assertClean(w.Local)
}

// A boundary on one fork does not reach the other. main ← a ← {b, c}: naming c
// decides c and what it is stacked on, and b is still refused.
//
// The boundary was a position in the selection, which is the tree flattened,
// so b -- drawn before c -- sat "below" it and was replaced with its published
// version, discarding the work on it that nobody had decided about.
func TestJourneyABoundaryOnOneForkDoesNotTakeItsSibling(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	w.branchOff("synthetic-a", "synthetic-c", "c.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a", "--apply")
	mustRun(t, "track", "--branch", "synthetic-c", "--parent", "synthetic-a", "--apply")
	w.git(w.Local, "push", "-q", "origin", "synthetic-a", "synthetic-b", "synthetic-c")

	// Both forks diverge: somebody publishes to each, and you amend each.
	w.git(w.Other, "fetch", "-q", "origin")
	for _, branch := range []string{"synthetic-b", "synthetic-c"} {
		w.git(w.Other, "switch", "-q", "-c", branch, "origin/"+branch)
		w.commit(w.Other, branch, "theirs-"+branch+".txt", "theirs")
		w.git(w.Other, "push", "-q", "origin", branch)
		w.commit(w.Local, branch, "mine-"+branch+".txt", "mine")
		w.git(w.Local, "commit", "-q", "--amend", "-m", "synthetic mine, revised")
	}
	w.git(w.Local, "switch", "-q", "synthetic-a")
	sibling := w.tip(w.Local, "synthetic-b")

	stdout, _, err := run(t, "pull", "--take", "published", "--through", "synthetic-c", "--apply")
	if err == nil {
		t.Fatalf("a divergence on the other fork was not refused:\n%s", stdout)
	}
	if now := w.tip(w.Local, "synthetic-b"); now != sibling {
		t.Errorf("synthetic-b moved from %s to %s · the boundary on synthetic-c reached its sibling", sibling, now)
	}
	w.assertHas(w.Local, "synthetic-b", "mine-synthetic-b.txt")
	// No single boundary covers both forks, so the way through drops it.
	if !strings.Contains(stdout+err.Error(), "g2g pull --take published to") {
		t.Errorf("the refusal does not offer the take that covers both forks:\n%s\n%v", stdout, err)
	}
	w.assertClean(w.Local)
}

// Work above the boundary that has not diverged is kept and replayed onto what
// was taken below it. Nothing consults --take for this branch at all: being
// ahead of your published version is push's business, so the boundary changes
// nothing here and the branch simply follows its parent.
func TestJourneyUnpushedWorkAboveTheBoundaryIsReplayedOntoWhatWasTaken(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a", "--apply")
	mustRun(t, "push", "--apply")

	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "switch", "-q", "-c", "synthetic-a", "origin/synthetic-a")
	w.commit(w.Other, "synthetic-a", "theirs.txt", "theirs")
	w.git(w.Other, "push", "-q", "origin", "synthetic-a")
	theirs := w.tip(w.Other, "synthetic-a")

	w.commit(w.Local, "synthetic-a", "mine.txt", "mine")
	w.git(w.Local, "commit", "-q", "--amend", "-m", "synthetic mine, revised")
	w.commit(w.Local, "synthetic-b", "b-new.txt", "b-new")
	w.git(w.Local, "switch", "-q", "synthetic-b")

	mustRun(t, "pull", "--take", "published", "--through", "synthetic-a", "--apply")

	if now := w.tip(w.Local, "synthetic-a"); now != theirs {
		t.Errorf("synthetic-a is at %s, want the published %s", now, theirs)
	}
	w.assertHas(w.Local, "synthetic-a", "theirs.txt")
	w.assertHas(w.Local, "synthetic-b", "b-new.txt")
	w.assertHas(w.Local, "synthetic-b", "theirs.txt")
	if !w.contains(w.Local, "synthetic-a", "synthetic-b") {
		t.Error("synthetic-b was not replayed onto the taken synthetic-a")
	}
	w.assertClean(w.Local)
}

// A boundary naming something this sync does not select resolves nothing, and
// would be indistinguishable from an ordinary refusal.
func TestJourneyABoundaryOutsideTheStackIsRefused(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")

	out, _, _ := run(t, "pull", "--take", "published", "--through", "synthetic-elsewhere")
	if !strings.Contains(out, "not in the stack being synced") {
		t.Errorf("a boundary outside the selection was not refused:\n%s", out)
	}
}

// An unknown value is refused before anything runs, and the refusal lists what
// the flag does take.
func TestJourneyAnUnknownTakeIsRefused(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")

	_, _, err := run(t, "pull", "--take", "synthetic-nonsense")
	if err == nil {
		t.Fatal("sync accepted --take synthetic-nonsense")
	}
	if !strings.Contains(err.Error(), "published") {
		t.Errorf("the refusal does not list what --take accepts: %v", err)
	}
}

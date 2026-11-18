package cli_test

import (
	"strings"
	"testing"

	localgit "github.com/shhac/g2g/internal/git"
)

// mergeCarryingStack is main ← synthetic-parent ← synthetic-child ←
// synthetic-top, where synthetic-child merged a branch from another stack with
// plain Git and synthetic-parent then gained a commit. It returns the merge
// commit, and leaves synthetic-child checked out.
func mergeCarryingStack(t *testing.T) (*world, string) {
	t.Helper()
	w := newWorld(t)
	for _, edge := range [][2]string{{"main", "synthetic-parent"}, {"synthetic-parent", "synthetic-child"}, {"synthetic-child", "synthetic-top"}} {
		w.branchOff(edge[0], edge[1], edge[1]+".txt")
		mustRun(t, "track", "--parent", edge[0], "--apply")
	}
	w.branchOff("main", "synthetic-dep", "synthetic-dep.txt")
	w.git(w.Local, "switch", "-q", "synthetic-child")
	w.git(w.Local, "merge", "-q", "--no-edit", "synthetic-dep")
	merge := w.tip(w.Local, "synthetic-child")
	w.commit(w.Local, "synthetic-child", "synthetic-child-2.txt", "synthetic-child-2.txt")
	w.commit(w.Local, "synthetic-parent", "synthetic-parent-2.txt", "synthetic-parent-2.txt")
	w.git(w.Local, "switch", "-q", "synthetic-child")
	return w, merge
}

// A replay cannot carry a merge commit: one engine stops on it and the other
// flattens it, making the merged branch's commits this branch's own. The
// preview used to die on the first engine's fatal; the plan refuses instead,
// before anything moves, naming the merge and the way to keep it.
func TestJourneyRestackRefusesAMergeInTheReplayRange(t *testing.T) {
	for _, scope := range []string{"branch", "stack"} {
		t.Run(scope, func(t *testing.T) {
			w, merge := mergeCarryingStack(t)
			store := w.readStore()
			child, top := w.tip(w.Local, "synthetic-child"), w.tip(w.Local, "synthetic-top")
			unmoved := func(when string) {
				t.Helper()
				if w.tip(w.Local, "synthetic-child") != child || w.tip(w.Local, "synthetic-top") != top {
					t.Errorf("%s moved a branch of a stack it refused", when)
				}
				if w.readStore() != store {
					t.Errorf("%s changed the record of a stack it refused", when)
				}
				w.assertClean(w.Local)
			}

			preview := mustRun(t, "restack", "--branch", "synthetic-child", "--scope", scope)
			for _, want := range []string{
				"merge commit " + localgit.Short(merge),
				"git merge synthetic-parent",
				"g2g track --branch synthetic-child --parent synthetic-parent",
			} {
				if !strings.Contains(preview, want) {
					t.Errorf("preview omits %q:\n%s", want, preview)
				}
			}
			unmoved("the preview")

			stdout, stderr, err := run(t, "restack", "--branch", "synthetic-child", "--scope", scope, "--apply")
			if err == nil {
				t.Fatalf("apply replayed a range holding a merge commit:\n%s", stdout)
			}
			if said := stdout + stderr + err.Error(); strings.Contains(said, "fatal") || !strings.Contains(said, "merge commit "+localgit.Short(merge)) {
				t.Errorf("apply does not refuse over the merge commit: %v\n%s%s", err, stdout, stderr)
			}
			unmoved("the refused apply")
		})
	}
}

// The way out the refusal names: merge the parent in with plain Git, and
// record that the branch now sits on it. track used to answer "Nothing to do"
// here, leaving the fork point at the parent's old tip and the branch reading
// as needing a restack that could never run.
func TestJourneyTrackRefreshesAForkPointBehindTheParentItContains(t *testing.T) {
	w, _ := mergeCarryingStack(t)
	old := w.tip(w.Local, "refs/g2g/forkpoints/synthetic-child")
	w.git(w.Local, "merge", "-q", "--no-edit", "synthetic-parent")
	seated, parent := w.tip(w.Local, "synthetic-child"), w.tip(w.Local, "synthetic-parent")

	preview := mustRun(t, "track", "--branch", "synthetic-child", "--parent", "synthetic-parent")
	if !strings.Contains(preview, localgit.Short(old)) || !strings.Contains(preview, localgit.Short(parent)) || strings.Contains(preview, "Nothing to do") {
		t.Errorf("track preview does not name the boundary it moves from %s to %s:\n%s", localgit.Short(old), localgit.Short(parent), preview)
	}
	if got := w.tip(w.Local, "refs/g2g/forkpoints/synthetic-child"); got != old {
		t.Fatal("track preview moved the fork point")
	}
	mustRun(t, "track", "--branch", "synthetic-child", "--parent", "synthetic-parent", "--apply")
	if got := w.tip(w.Local, "refs/g2g/forkpoints/synthetic-child"); got != parent {
		t.Errorf("fork point = %s, want synthetic-parent's tip %s", got, parent)
	}
	if status := mustRun(t, "status", "--branch", "synthetic-child", "--scope", "branch"); strings.Contains(status, "needs restack") {
		t.Errorf("a branch holding its parent's tip still reads as needing a restack:\n%s", status)
	}
	w.assertClean(w.Local)

	// What is stacked on it replays onto it as it is, merges and all.
	mustRun(t, "restack", "--branch", "synthetic-child", "--scope", "stack", "--apply")
	w.assertClean(w.Local)
	if got := w.tip(w.Local, "synthetic-child"); got != seated {
		t.Errorf("synthetic-child was rewritten to %s, want it left at %s", got, seated)
	}
	if !w.contains(w.Local, "synthetic-child", "synthetic-top") {
		t.Error("synthetic-top was not replayed onto synthetic-child")
	}
	if status := mustRun(t, "status"); strings.Contains(status, "needs restack") {
		t.Errorf("the stack still reads as needing a restack:\n%s", status)
	}
}

// restack reaches the same record by itself: a branch that already holds its
// parent's tip has nothing to replay, so only where it forks is recorded and
// no commit is rewritten. It used to replay the branch onto the commit it
// already sat on, which gave every commit a new id and stopped on the merge.
func TestJourneyRestackRecordsABranchAlreadyOnItsParentWithoutRewritingIt(t *testing.T) {
	w, _ := mergeCarryingStack(t)
	w.git(w.Local, "merge", "-q", "--no-edit", "synthetic-parent")
	seated, parent := w.tip(w.Local, "synthetic-child"), w.tip(w.Local, "synthetic-parent")
	structure := w.readStructure()

	preview := mustRun(t, "restack", "--branch", "synthetic-child", "--scope", "branch")
	if !strings.Contains(preview, "synthetic-child already holds synthetic-parent") || strings.Contains(preview, "Replays") {
		t.Errorf("preview does not say only the fork point is recorded:\n%s", preview)
	}
	if got := w.tip(w.Local, "refs/g2g/forkpoints/synthetic-child"); got == parent {
		t.Fatal("restack preview moved the fork point")
	}
	mustRun(t, "restack", "--branch", "synthetic-child", "--scope", "branch", "--apply")
	w.assertClean(w.Local)
	if got := w.tip(w.Local, "synthetic-child"); got != seated {
		t.Errorf("synthetic-child was rewritten to %s, want it left at %s", got, seated)
	}
	if got := w.tip(w.Local, "refs/g2g/forkpoints/synthetic-child"); got != parent {
		t.Errorf("fork point = %s, want synthetic-parent's tip %s", got, parent)
	}
	if got := w.readStructure(); got["synthetic-child"] != structure["synthetic-child"] || got["trunks"] != structure["trunks"] {
		t.Errorf("structure changed: %v, want %v", got, structure)
	}
	if status := mustRun(t, "status", "--branch", "synthetic-child", "--scope", "branch"); strings.Contains(status, "needs restack") {
		t.Errorf("a branch holding its parent's tip still reads as needing a restack:\n%s", status)
	}
}

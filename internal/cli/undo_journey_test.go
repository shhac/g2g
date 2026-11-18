package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// standing is everything an undo answers for: every local branch, the fork
// points g2g pins, the recorded graph and where the checkout is.
//
// Sync points are left out and checked on their own. A command that finds a
// branch level with its remote says so while it plans, before anything it
// changes, and that stays true whatever is undone afterwards.
func (w *world) standing(t *testing.T) string {
	t.Helper()
	refs := []string{}
	for _, line := range strings.Split(w.git(w.Local, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads", "refs/g2g"), "\n") {
		if !strings.HasPrefix(line, "refs/g2g/remotes/") && !strings.HasPrefix(line, "refs/g2g/synced/") {
			refs = append(refs, line)
		}
	}
	return strings.Join(refs, "\n") + "\n" + w.readStore() + "\n" + w.git(w.Local, "symbolic-ref", "HEAD")
}

// syncPoints is each sync point, by remote and branch, with the commit it
// says this checkout and the remote agreed on.
func (w *world) syncPoints() map[string]string {
	points := map[string]string{}
	for _, line := range strings.Split(w.git(w.Local, "for-each-ref", "--format=%(refname:lstrip=3) %(objectname)", "refs/g2g/synced"), "\n") {
		if ref, agreed, found := strings.Cut(line, " "); found {
			points[ref] = agreed
		}
	}
	return points
}

// undoWorld is a recorded stack of two on a trunk that has moved on, with
// the bottom branch squash-merged into the remote's trunk but not yet pulled.
func undoWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a", "--apply")
	w.git(w.Local, "push", "-q", "origin", "synthetic-a", "synthetic-b")
	w.commit(w.Other, "main", "colleague.txt", "synthetic upstream")
	w.git(w.Other, "push", "-q", "origin", "main")
	w.git(w.Local, "switch", "-q", "synthetic-b")
	return w
}

// Each operation that changes only this repository is put back whole by one
// undo: every branch, every ref g2g keeps, the record and the checkout are
// exactly as they were before it, and nothing is left in the working tree.
func TestJourneyUndoPutsBackWhatEachOperationChanged(t *testing.T) {
	operations := map[string]struct {
		prepare func(w *world)
		run     []string
	}{
		"restack onto a trunk that moved": {func(w *world) { mustRun(t, "pull", "--trunk-only", "--apply") }, []string{"restack", "--scope", "stack", "--apply"}},
		"pull":                            {nil, []string{"pull", "--apply"}},
		"track onto another parent":       {nil, []string{"track", "--branch", "synthetic-b", "--parent", "main", "--apply"}},
		"untrack":                         {nil, []string{"untrack", "--branch", "synthetic-b", "--apply"}},
		"create":                          {nil, []string{"create", "synthetic-c", "--apply"}},
		"rename":                          {nil, []string{"rename", "synthetic-renamed", "--apply"}},
		"delete":                          {nil, []string{"delete", "--branch", "synthetic-a", "--apply"}},
		"fold":                            {nil, []string{"fold", "--branch", "synthetic-b", "--apply"}},
		"adopt": {func(w *world) {
			mustRun(t, "untrack", "--branch", "synthetic-b", "--apply")
			mustRun(t, "untrack", "--branch", "synthetic-a", "--apply")
		}, []string{"adopt", "--apply"}},
		"prune a landed branch and delete it": {func(w *world) {
			w.git(w.Other, "fetch", "-q", "origin")
			w.git(w.Other, "merge", "-q", "--squash", "origin/synthetic-a")
			w.git(w.Other, "commit", "-qm", "synthetic squash")
			w.git(w.Other, "push", "-q", "origin", "main")
			mustRun(t, "pull", "--apply")
		}, []string{"prune", "--delete-branches", "--apply"}},
	}
	for name, each := range operations {
		t.Run(name, func(t *testing.T) {
			w := undoWorld(t)
			if each.prepare != nil {
				each.prepare(w)
			}
			before, synced := w.standing(t), w.syncPoints()
			out, stderr, err := run(t, each.run...)
			if err != nil {
				t.Fatalf("g2g %s: %v\n%s%s", strings.Join(each.run, " "), err, out, stderr)
			}
			if w.standing(t) == before {
				t.Fatalf("g2g %s changed nothing to undo:\n%s", strings.Join(each.run, " "), out)
			}
			preview := mustRun(t, "undo")
			if !strings.Contains(preview, "g2g "+each.run[0]) || !strings.Contains(preview, "Rerun with --apply to put it back.") {
				t.Errorf("preview does not name the operation:\n%s", preview)
			}
			mustRun(t, "undo", "--apply")
			if after := w.standing(t); after != before {
				t.Errorf("after the undo:\n%s\nbefore the operation:\n%s", after, before)
			}
			w.assertClean(w.Local)
			// No sync point the operation made is left saying this checkout
			// agreed with the remote on a commit the branch no longer has:
			// that is what would read the commits just put back as dropped
			// here. One made while planning, for a branch level with its
			// remote, names the commit the branch is back on.
			for ref, agreed := range w.syncPoints() {
				branch := strings.SplitN(ref, "/", 2)[1]
				if agreed == synced[ref] || w.git(w.Local, "for-each-ref", "refs/heads/"+branch) == "" {
					continue
				}
				if !w.contains(w.Local, agreed, branch) {
					t.Errorf("a sync point for %s is left at %s, which the branch does not hold", branch, agreed)
				}
			}
		})
	}
}

// Undo never undoes work done since. Whatever the operation changed that has
// changed again refuses it, named, with nothing moved.
func TestJourneyUndoIsRefusedOverWorkDoneSince(t *testing.T) {
	changes := map[string]struct {
		change func(w *world)
		want   string
	}{
		"a commit on a branch it moved": {func(w *world) { w.commit(w.Local, "synthetic-b", "later.txt", "synthetic later") }, "synthetic-b has moved since, from "},
		"the record edited again": {func(w *world) {
			mustRun(t, "track", "--branch", "synthetic-b", "--parent", "main", "--apply")
			// That is an operation too, and the newest; undo it by hand so
			// the restack is the one offered and its record is what moved.
			w.rewriteStore(func(store string) string { return store })
		}, ""},
		"a branch it moved was deleted": {func(w *world) {
			w.git(w.Local, "switch", "-q", "main")
			w.git(w.Local, "branch", "-q", "-D", "synthetic-b")
		}, "synthetic-b has been deleted since"},
	}
	for name, each := range changes {
		if each.want == "" {
			continue
		}
		t.Run(name, func(t *testing.T) {
			w := undoWorld(t)
			mustRun(t, "pull", "--trunk-only", "--apply")
			mustRun(t, "restack", "--scope", "stack", "--apply")
			each.change(w)
			standing := w.standing(t)
			preview := mustRun(t, "undo")
			if !strings.Contains(preview, "Apply blocked") || !strings.Contains(preview, each.want) {
				t.Errorf("preview lacks the refusal %q:\n%s", each.want, preview)
			}
			if _, _, err := run(t, "undo", "--apply"); err == nil {
				t.Error("an undo over work done since was applied")
			}
			if w.standing(t) != standing {
				t.Error("a refused undo changed something")
			}
		})
	}
}

// A branch another worktree has checked out is not moved from under it, and
// one with uncommitted changes here is not moved over them.
func TestJourneyUndoLeavesWorktreesItCannotBringAlong(t *testing.T) {
	w := undoWorld(t)
	mustRun(t, "pull", "--trunk-only", "--apply")
	mustRun(t, "restack", "--scope", "stack", "--apply")
	elsewhere := filepath.Join(t.TempDir(), "synthetic-elsewhere")
	w.git(w.Local, "worktree", "add", "-q", elsewhere, "synthetic-a")
	if preview := mustRun(t, "undo"); !strings.Contains(preview, "synthetic-a is checked out in ") {
		t.Errorf("preview does not refuse the branch held elsewhere:\n%s", preview)
	}
	w.git(w.Local, "worktree", "remove", elsewhere)

	if err := os.WriteFile(filepath.Join(w.Local, "b.txt"), []byte("synthetic uncommitted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	standing := w.standing(t)
	if out, _, err := run(t, "undo", "--apply"); err == nil {
		t.Errorf("an undo moved a checked-out branch over uncommitted changes:\n%s", out)
	}
	if w.standing(t) != standing {
		t.Error("a refused undo changed something")
	}
	if content, _ := os.ReadFile(filepath.Join(w.Local, "b.txt")); string(content) != "synthetic uncommitted\n" {
		t.Error("uncommitted work was lost")
	}
}

// Undo walks back one operation at a time, newest first, and says when there
// is nothing left. What was published is never recorded as something to undo.
func TestJourneyUndoWalksBackThroughOperations(t *testing.T) {
	w := undoWorld(t)
	start := w.standing(t)
	mustRun(t, "pull", "--trunk-only", "--apply")
	afterPull := w.standing(t)
	mustRun(t, "restack", "--scope", "stack", "--apply")

	list := mustRun(t, "undo", "--list")
	if !strings.Contains(list, "g2g restack --scope=stack · next to undo") || !strings.Contains(list, "g2g pull --trunk-only=true") {
		t.Errorf("the list of operations:\n%s", list)
	}
	mustRun(t, "undo", "--apply")
	if got := w.standing(t); got != afterPull {
		t.Errorf("after undoing the restack:\n%s\nwant:\n%s", got, afterPull)
	}
	mustRun(t, "undo", "--apply")
	if got := w.standing(t); got != start {
		t.Errorf("after undoing the pull:\n%s\nwant:\n%s", got, start)
	}
	// The tracks that built the world are older operations, and are offered
	// in turn; walk them back and nothing is left.
	for range 2 {
		mustRun(t, "undo", "--apply")
	}
	if out := mustRun(t, "undo"); !strings.Contains(out, "Nothing to undo") {
		t.Errorf("with every operation put back:\n%s", out)
	}
	if list := mustRun(t, "undo", "--list"); strings.Contains(list, "next to undo") || !strings.Contains(list, "undone") {
		t.Errorf("the list once everything is undone:\n%s", list)
	}
	w.assertClean(w.Local)
}

// What has been published since is not put back from here: the remote would
// be left holding what this checkout no longer had. Undo says so and stops.
func TestJourneyUndoStopsAtWhatHasBeenPublishedSince(t *testing.T) {
	w := undoWorld(t)
	mustRun(t, "pull", "--trunk-only", "--apply")
	mustRun(t, "restack", "--scope", "stack", "--apply")
	mustRun(t, "push", "--apply")
	standing := w.standing(t)

	if list := mustRun(t, "undo", "--list"); !strings.Contains(list, "g2g push · published, not undone from here") {
		t.Errorf("the list does not show the publication:\n%s", list)
	}
	preview := mustRun(t, "undo")
	if !strings.Contains(preview, "Apply blocked") || !strings.Contains(preview, "has been published since g2g restack") {
		t.Errorf("preview does not stop at the publication:\n%s", preview)
	}
	if _, _, err := run(t, "undo", "--apply"); err == nil {
		t.Error("an undo went back past a publication")
	}
	if w.standing(t) != standing {
		t.Error("a refused undo changed something")
	}
}

// A branch created with a commit holds work that is on nothing else. Removing
// it reported everything as it was before, with the file gone and its commit
// on no branch. It is refused, and keeping the commit under another name is
// the way through.
func TestJourneyUndoDoesNotRemoveABranchHoldingTheOnlyCopyOfItsWork(t *testing.T) {
	w := undoWorld(t)
	if err := os.WriteFile(filepath.Join(w.Local, "staged.txt"), []byte("synthetic staged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.git(w.Local, "add", "staged.txt")
	mustRun(t, "create", "synthetic-c", "-m", "synthetic staged work", "--apply")
	made := w.tip(w.Local, "synthetic-c")

	preview := mustRun(t, "undo")
	for _, want := range []string{"Apply blocked", "holds commits no other branch has", "git branch synthetic-c-kept synthetic-c"} {
		if !strings.Contains(preview, want) {
			t.Errorf("preview lacks %q:\n%s", want, preview)
		}
	}
	if !strings.Contains(preview, "g2g create synthetic-c") {
		t.Errorf("the operation is not named with the branch it created:\n%s", preview)
	}
	if _, _, err := run(t, "undo", "--apply"); err == nil || w.tip(w.Local, "synthetic-c") != made {
		t.Fatalf("an undo removed the only branch holding a commit: %v", err)
	}

	w.git(w.Local, "branch", "synthetic-c-kept", "synthetic-c")
	mustRun(t, "undo", "--apply")
	if w.git(w.Local, "for-each-ref", "refs/heads/synthetic-c") != "" || w.tip(w.Local, "synthetic-c-kept") != made {
		t.Error("the branch was not removed with its commit kept under the other name")
	}
	w.assertClean(w.Local)
}

// An operation run from a detached HEAD is put back to a detached HEAD.
func TestJourneyUndoReturnsToADetachedCheckout(t *testing.T) {
	w := undoWorld(t)
	w.git(w.Local, "switch", "-q", "--detach", "synthetic-b")
	detached := w.tip(w.Local, "HEAD")
	mustRun(t, "create", "synthetic-c", "--parent", "synthetic-b", "--apply")
	mustRun(t, "undo", "--apply")
	if w.git(w.Local, "for-each-ref", "refs/heads/synthetic-c") != "" {
		t.Error("the created branch survived the undo")
	}
	if w.git(w.Local, "rev-parse", "--abbrev-ref", "HEAD") != "HEAD" || w.tip(w.Local, "HEAD") != detached {
		t.Error("the checkout is not detached where it was")
	}
}

// A record is put back only for the branch it was about. One whose record
// alone changed and which was then rebased with plain Git would get back a
// fork point it no longer holds.
func TestJourneyUndoOfARecordIsRefusedOnceTheBranchMoved(t *testing.T) {
	w := undoWorld(t)
	mustRun(t, "untrack", "--branch", "synthetic-b", "--apply")
	w.commit(w.Local, "synthetic-b", "later.txt", "synthetic later")
	store := w.readStore()
	preview := mustRun(t, "undo")
	if !strings.Contains(preview, "Apply blocked") || !strings.Contains(preview, "synthetic-b has moved since") {
		t.Errorf("preview does not refuse over the branch that moved:\n%s", preview)
	}
	if _, _, err := run(t, "undo", "--apply"); err == nil || w.readStore() != store {
		t.Errorf("a record was put back for a branch that had moved: %v", err)
	}
}

// What a branch has to be put back at has to still exist. Asked ref by ref,
// the first commit Git had collected stopped an undo half-way, with nothing
// that could finish it.
func TestJourneyUndoIsRefusedWholeWhenACommitIsGone(t *testing.T) {
	w := undoWorld(t)
	mustRun(t, "delete", "--branch", "synthetic-b", "--apply")
	w.git(w.Local, "reflog", "expire", "--expire=now", "--all")
	w.git(w.Local, "update-ref", "-d", "refs/remotes/origin/synthetic-b")
	w.git(w.Local, "gc", "-q", "--prune=now")
	standing := w.standing(t)
	preview := mustRun(t, "undo")
	if !strings.Contains(preview, "Apply blocked") || !strings.Contains(preview, "is no longer in this repository") {
		t.Errorf("preview does not say the commit is gone:\n%s", preview)
	}
	if _, _, err := run(t, "undo", "--apply"); err == nil || w.standing(t) != standing {
		t.Errorf("an undo with a commit gone changed something: %v", err)
	}
}

// A rename is put back for a branch nothing else holds: the old name comes
// back with everything the new one had. Asking whether the new name was the
// only branch with its commits refused every rename of an unpushed branch.
func TestJourneyUndoPutsBackTheRenameOfAnUnpushedBranch(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	before := w.standing(t)
	mustRun(t, "rename", "synthetic-renamed", "--apply")
	mustRun(t, "undo", "--apply")
	if after := w.standing(t); after != before {
		t.Errorf("after the undo:\n%s\nbefore the rename:\n%s", after, before)
	}
	w.assertClean(w.Local)
}

// The log is shared by every worktree, and each may be somebody else's
// session. An undo offers only what was done from the worktree it is run in.
func TestJourneyUndoOffersOnlyThisWorktreesOperations(t *testing.T) {
	w := undoWorld(t)
	elsewhere := filepath.Join(t.TempDir(), "synthetic-elsewhere")
	w.git(w.Local, "worktree", "add", "-q", "-b", "synthetic-theirs", elsewhere, "main")
	t.Chdir(elsewhere)
	mustRun(t, "track", "--branch", "synthetic-theirs", "--parent", "main", "--apply")
	t.Chdir(w.Local)
	store := w.readStore()

	list := mustRun(t, "undo", "--list")
	if !strings.Contains(list, "--branch=synthetic-theirs --parent=main · from another worktree") {
		t.Errorf("the list does not mark the other worktree's operation:\n%s", list)
	}
	preview := mustRun(t, "undo")
	if strings.Contains(preview, "synthetic-theirs") || !strings.Contains(preview, "--branch=synthetic-b") {
		t.Errorf("an undo here offers something other than this worktree's last operation:\n%s", preview)
	}
	mustRun(t, "undo", "--apply")
	if !strings.Contains(w.readStore(), "synthetic-theirs") || w.readStore() == store {
		t.Error("the undo here did not put back this worktree's operation alone")
	}
}

package cli_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/testutil"
)

// delete, fold and rename move and remove refs under the checkout, which is
// the class of change a PATH fake cannot vouch for: the recurring bug is a ref
// moving while the tree, the index or another worktree stays behind. These run
// against real Git, and every mutation is followed by a clean-tree check and
// the recorded structure it left.

// reshapeWorld is a recorded stack with a fork in it:
//
//	main
//	└─ synthetic-a
//	   ├─ synthetic-b
//	   │  └─ synthetic-c
//	   └─ synthetic-side
func reshapeWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	w.branchOff("synthetic-b", "synthetic-c", "c.txt")
	w.branchOff("synthetic-a", "synthetic-side", "side.txt")
	for branch, parent := range map[string]string{
		"synthetic-a": "main", "synthetic-b": "synthetic-a", "synthetic-c": "synthetic-b", "synthetic-side": "synthetic-a",
	} {
		mustRun(t, "track", "--branch", branch, "--parent", parent, "--apply")
	}
	return w
}

func (w *world) assertStructure(want map[string]string) {
	w.t.Helper()
	structure := w.readStructure()
	for branch, parent := range want {
		if structure[branch] != parent {
			w.t.Errorf("the graph records %s as %q, want %q (%v)", branch, structure[branch], parent, structure)
		}
	}
}

func (w *world) hasRef(ref string) bool {
	w.t.Helper()
	return w.git(w.Local, "for-each-ref", "--format=%(refname)", ref) != ""
}

// Deleting a branch in the middle puts what sat on it on what it sat on, and
// the next restack replays only the child's own commits there — so the deleted
// branch's work is gone from it, which is what the preview said would happen.
func TestDeleteRecordsTheChildrenOnTheParentAndRestackDropsItsCommits(t *testing.T) {
	w := reshapeWorld(t)
	w.git(w.Local, "switch", "-q", "synthetic-b")

	preview := mustRun(t, "delete")
	for _, want := range []string{
		"deleted", "moves onto synthetic-a",
		"has 1 commit that exists nowhere else", "synthetic b.txt",
		"g2g restack --branch synthetic-c", "deletes the local branch only",
		"Rerun with --apply",
	} {
		if !strings.Contains(preview, want) {
			t.Errorf("preview omits %q:\n%s", want, preview)
		}
	}
	if !w.hasRef("refs/heads/synthetic-b") {
		t.Fatal("a preview deleted the branch")
	}

	applied := mustRun(t, "delete", "--apply")
	w.assertClean(w.Local)
	if !strings.Contains(applied, "Suggested next step") || !strings.Contains(applied, "g2g restack --branch synthetic-c") {
		t.Errorf("apply does not suggest the restack that finishes it:\n%s", applied)
	}
	if current := w.git(w.Local, "branch", "--show-current"); current != "synthetic-a" {
		t.Errorf("checked out %q, want the deleted branch's parent", current)
	}
	if w.hasRef("refs/heads/synthetic-b") || w.hasRef("refs/g2g/forkpoints/synthetic-b") {
		t.Error("the branch or its fork-point pin survived the delete")
	}
	w.assertStructure(map[string]string{"synthetic-c": "synthetic-a", "synthetic-side": "synthetic-a", "synthetic-b": ""})

	mustRun(t, "restack", "--branch", "synthetic-c", "--apply")
	w.assertClean(w.Local)
	w.assertHas(w.Local, "synthetic-c", "c.txt")
	if err := w.tryGit("cat-file", "-e", "synthetic-c:b.txt"); err == nil {
		t.Error("synthetic-c still carries the deleted branch's work after a restack")
	}
	if !w.contains(w.Local, "synthetic-a", "synthetic-c") {
		t.Error("synthetic-c was not replayed onto synthetic-a")
	}
}

// Work that is already in the parent or on a remote is not lost by a delete,
// and the preview must not say it is.
func TestDeleteOfPublishedWorkLosesNothing(t *testing.T) {
	w := reshapeWorld(t)
	w.git(w.Local, "push", "-q", "origin", "synthetic-c")

	preview := mustRun(t, "delete", "--branch", "synthetic-c")

	if !strings.Contains(preview, "loses no commit") || strings.Contains(preview, "nowhere else") {
		t.Errorf("preview does not say the published branch loses nothing:\n%s", preview)
	}
	if !strings.Contains(preview, "origin/synthetic-c is untouched") {
		t.Errorf("preview does not say the remote branch stays:\n%s", preview)
	}
}

func TestDeleteRefusesABranchAnotherWorktreeHasAndATrunk(t *testing.T) {
	w := reshapeWorld(t)
	w.git(w.Local, "switch", "-q", "synthetic-a")
	w.git(w.Local, "worktree", "add", "-q", filepath.Join(filepath.Dir(w.Local), "held"), "synthetic-c")

	for _, test := range []struct {
		args []string
		want string
	}{
		{args: []string{"delete", "--branch", "synthetic-c"}, want: "checked out in another worktree"},
		{args: []string{"delete", "--branch", "main"}, want: "is a trunk"},
	} {
		preview := mustRun(t, test.args...)
		if !strings.Contains(preview, test.want) {
			t.Errorf("%v preview does not refuse with %q:\n%s", test.args, test.want, preview)
		}
		if _, _, err := run(t, append(test.args, "--apply")...); err == nil {
			t.Errorf("%v --apply succeeded", test.args)
		}
	}
	if !w.hasRef("refs/heads/synthetic-c") || !w.hasRef("refs/heads/main") {
		t.Error("a refused delete removed a branch")
	}
}

// Folding into the branch the checkout is on moves that branch's ref, so the
// tree has to come with it; a sibling left on the old tip needs a restack and
// the graph says so.
func TestFoldFastForwardsTheParentAndTheCheckoutFollows(t *testing.T) {
	w := reshapeWorld(t)
	w.git(w.Local, "switch", "-q", "synthetic-a")
	folded := w.tip(w.Local, "synthetic-b")

	preview := mustRun(t, "fold", "--branch", "synthetic-b")
	for _, want := range []string{"folds into synthetic-a", "Fast-forwards synthetic-a", "synthetic-side also sits on synthetic-a", "g2g restack --branch synthetic-side"} {
		if !strings.Contains(preview, want) {
			t.Errorf("preview omits %q:\n%s", want, preview)
		}
	}

	mustRun(t, "fold", "--branch", "synthetic-b", "--apply")

	w.assertClean(w.Local)
	if w.tip(w.Local, "synthetic-a") != folded {
		t.Error("synthetic-a was not fast-forwarded to synthetic-b")
	}
	w.assertHas(w.Local, "synthetic-a", "b.txt")
	if _, err := os.Stat(filepath.Join(w.Local, "b.txt")); err != nil {
		t.Errorf("the working tree did not follow synthetic-a: %v", err)
	}
	if w.hasRef("refs/heads/synthetic-b") {
		t.Error("synthetic-b survived the fold")
	}
	w.assertStructure(map[string]string{"synthetic-c": "synthetic-a", "synthetic-side": "synthetic-a", "synthetic-a": "main"})
	graph := mustRun(t, "graph", "--branch", "synthetic-side")
	if !strings.Contains(graph, "needs restack") {
		t.Errorf("the sibling left behind does not read as needing a restack:\n%s", graph)
	}
}

// Folding the branch you stand on switches to the parent once the parent has
// arrived at the same commit, which cannot disturb anything.
func TestFoldingTheCheckedOutBranchLeavesYouOnTheParent(t *testing.T) {
	w := reshapeWorld(t)
	w.git(w.Local, "switch", "-q", "synthetic-c")

	mustRun(t, "fold", "--apply")

	w.assertClean(w.Local)
	if current := w.git(w.Local, "branch", "--show-current"); current != "synthetic-b" {
		t.Errorf("checked out %q, want synthetic-b", current)
	}
	w.assertHas(w.Local, "synthetic-b", "c.txt")
}

func TestFoldRefusesATrunkAndAParentThatMovedOn(t *testing.T) {
	w := reshapeWorld(t)
	w.commit(w.Local, "synthetic-b", "later.txt", "later")

	for args, want := range map[string]string{
		"synthetic-a": "g2g land --branch synthetic-a",
		"synthetic-c": "g2g restack --branch synthetic-c",
	} {
		preview := mustRun(t, "fold", "--branch", args)
		if !strings.Contains(preview, want) {
			t.Errorf("fold --branch %s does not name %q:\n%s", args, want, preview)
		}
	}
}

// read-tree will not overwrite a file it does not know about, so the fold
// stops there, and the parent's ref it had already moved goes back.
func TestAFoldTheTreeCannotFollowIsPutBack(t *testing.T) {
	w := reshapeWorld(t)
	w.git(w.Local, "switch", "-q", "synthetic-a")
	before := w.tip(w.Local, "synthetic-a")
	store := w.readStore()
	if err := os.WriteFile(filepath.Join(w.Local, "b.txt"), []byte("synthetic local\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := run(t, "fold", "--branch", "synthetic-b", "--apply")

	if err == nil {
		t.Fatalf("fold over an untracked file in the way succeeded:\n%s", stdout)
	}
	if !strings.Contains(err.Error(), "put back") {
		t.Errorf("error does not say it was put back: %v", err)
	}
	if w.tip(w.Local, "synthetic-a") != before {
		t.Error("synthetic-a was left moved")
	}
	if !w.hasRef("refs/heads/synthetic-b") || w.readStore() != store {
		t.Error("the branch or the record was changed by a fold that was put back")
	}
	if status := w.git(w.Local, "status", "--porcelain"); status != "?? b.txt" {
		t.Errorf("status = %q, want only the file that was in the way", status)
	}
}

func TestRenameMovesTheBranchItsRecordsAndItsPin(t *testing.T) {
	w := reshapeWorld(t)
	w.git(w.Local, "switch", "-q", "synthetic-b")
	w.git(w.Local, "push", "-q", "origin", "synthetic-b")

	preview := mustRun(t, "rename", "synthetic-renamed")
	for _, want := range []string{"becomes synthetic-renamed", "origin/synthetic-b still carries the old name", "g2g push publishes synthetic-renamed as a new branch"} {
		if !strings.Contains(preview, want) {
			t.Errorf("preview omits %q:\n%s", want, preview)
		}
	}

	mustRun(t, "rename", "synthetic-renamed", "--apply")

	w.assertClean(w.Local)
	if current := w.git(w.Local, "branch", "--show-current"); current != "synthetic-renamed" {
		t.Errorf("checked out %q, want the new name", current)
	}
	w.assertStructure(map[string]string{"synthetic-renamed": "synthetic-a", "synthetic-c": "synthetic-renamed", "synthetic-b": ""})
	if !w.hasRef("refs/g2g/forkpoints/synthetic-renamed") || w.hasRef("refs/g2g/forkpoints/synthetic-b") {
		t.Error("the fork-point pin did not move with the name")
	}
	if !w.hasRef("refs/remotes/origin/synthetic-b") {
		t.Error("the remote-tracking ref was touched")
	}
	graph := mustRun(t, "graph")
	if !strings.Contains(graph, "synthetic-renamed") || strings.Contains(graph, "needs restack") {
		t.Errorf("the renamed stack does not read as it did before:\n%s", graph)
	}
}

// git moves another worktree's HEAD with the branch it renames, so a rename
// there is allowed and leaves that worktree clean on the new name.
func TestRenamingABranchAnotherWorktreeHasCarriesItAlong(t *testing.T) {
	w := reshapeWorld(t)
	w.git(w.Local, "switch", "-q", "synthetic-a")
	held := filepath.Join(filepath.Dir(w.Local), "held")
	w.git(w.Local, "worktree", "add", "-q", held, "synthetic-c")

	mustRun(t, "rename", "--branch", "synthetic-c", "synthetic-renamed", "--apply")

	w.assertClean(held)
	if current := w.git(held, "branch", "--show-current"); current != "synthetic-renamed" {
		t.Errorf("the other worktree is on %q", current)
	}
	w.assertStructure(map[string]string{"synthetic-renamed": "synthetic-b"})
}

func TestRenameRefusesATakenOrInvalidName(t *testing.T) {
	w := reshapeWorld(t)

	for name, want := range map[string]string{
		"synthetic-a":   "already exists",
		"synthetic..no": "not a valid branch name",
	} {
		preview := mustRun(t, "rename", "--branch", "synthetic-b", name)
		if !strings.Contains(preview, want) {
			t.Errorf("rename to %s does not refuse with %q:\n%s", name, want, preview)
		}
	}
	// An option-like name is refused before git sees it, and cobra must not
	// read it as a flag either, hence the separator.
	preview := mustRun(t, "rename", "--branch", "synthetic-b", "--", "-synthetic")
	if !strings.Contains(preview, "cannot be passed safely") {
		t.Errorf("an option-like name was not refused:\n%s", preview)
	}
	if !w.hasRef("refs/heads/synthetic-b") {
		t.Error("a refused rename renamed the branch")
	}
}

func (w *world) tryGit(args ...string) error {
	w.t.Helper()
	command := exec.Command("git", args...)
	command.Dir, command.Env = w.Local, testutil.SyntheticGitEnv()
	return command.Run()
}

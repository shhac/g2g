package git

import (
	"context"
	"os"
	"testing"

	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
)

// A PATH fake proves nothing here: the question is which branches Git itself
// reports as checked out, and a fake answers whatever it is asked.
func TestCheckedOutElsewhereNamesOtherWorktreesAndNotThisOne(t *testing.T) {
	repo := testutil.NewGitRepo(t, "synthetic-trunk")
	repo.Commit("synthetic root", "root.txt", "root")
	repo.Run("branch", "synthetic-held")
	other := t.TempDir() + "/held"
	repo.Run("worktree", "add", "-q", other, "synthetic-held")

	inRepo(t, repo.Dir)
	elsewhere, err := Client{Runner: subprocess.ExecRunner{}}.CheckedOutElsewhere(context.Background())
	if err != nil {
		t.Fatalf("CheckedOutElsewhere() error = %v", err)
	}

	if elsewhere["synthetic-held"] == "" {
		t.Errorf("a branch checked out in another worktree was not reported: %v", elsewhere)
	}
	// The branch you are standing on is ordinary to rewrite, and reporting it
	// would refuse every restack in a repository that has any worktree at all.
	if path, reported := elsewhere["synthetic-trunk"]; reported {
		t.Errorf("the current worktree's own branch was reported as held by %q", path)
	}
}

// A repository with one worktree is the ordinary case and must produce nothing
// to refuse on.
func TestASingleWorktreeHoldsNothingElsewhere(t *testing.T) {
	repo := testutil.NewGitRepo(t, "synthetic-trunk")
	repo.Commit("synthetic root", "root.txt", "root")

	inRepo(t, repo.Dir)
	elsewhere, err := Client{Runner: subprocess.ExecRunner{}}.CheckedOutElsewhere(context.Background())
	if err != nil {
		t.Fatalf("CheckedOutElsewhere() error = %v", err)
	}
	if len(elsewhere) != 0 {
		t.Errorf("CheckedOutElsewhere() = %v, want empty", elsewhere)
	}
}

// A detached worktree has no branch for a rewrite to move underneath it, so it
// cannot conflict and must not be reported.
func TestADetachedWorktreeIsNotAConflict(t *testing.T) {
	repo := testutil.NewGitRepo(t, "synthetic-trunk")
	repo.Commit("synthetic root", "root.txt", "root")
	other := t.TempDir() + "/detached"
	repo.Run("worktree", "add", "-q", "--detach", other)

	inRepo(t, repo.Dir)
	elsewhere, err := Client{Runner: subprocess.ExecRunner{}}.CheckedOutElsewhere(context.Background())
	if err != nil {
		t.Fatalf("CheckedOutElsewhere() error = %v", err)
	}
	if len(elsewhere) != 0 {
		t.Errorf("a detached worktree was reported as holding %v", elsewhere)
	}
}

// A rebase detaches the worktree running it, so git worktree list calls it
// detached, while Git itself refuses to move the branch underneath it. Missing
// it let a rewrite here move a branch somebody's rebase was about to write.
func TestABranchAnotherWorktreeIsRebasingIsHeldAndBusy(t *testing.T) {
	repo := testutil.NewGitRepo(t, "synthetic-trunk")
	repo.Commit("synthetic root", "root.txt", "root")
	repo.Run("branch", "synthetic-held")
	repo.Commit("synthetic trunk change", "root.txt", "trunk")
	elsewhere := repo.Worktree("synthetic-held")
	elsewhere.Commit("synthetic conflicting change", "root.txt", "held")
	if err := elsewhere.Try("rebase", "synthetic-trunk"); err == nil {
		t.Fatal("the synthetic rebase did not stop on its conflict")
	}

	inRepo(t, repo.Dir)
	holders, err := Client{Runner: subprocess.ExecRunner{}}.HoldersElsewhere(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	holder, held := holders["synthetic-held"]
	if !held || holder.Busy != "rebase" || holder.Path == "" {
		t.Errorf("HoldersElsewhere() = %+v, want synthetic-held busy with a rebase", holders)
	}
	paths, err := Client{Runner: subprocess.ExecRunner{}}.BranchHolders(context.Background())
	if err != nil || paths["synthetic-held"] == "" {
		t.Errorf("BranchHolders() = %v, %v: a deletion would not see the rebase either", paths, err)
	}
}

func TestWorktreeCleanCountsUntrackedFiles(t *testing.T) {
	repo := testutil.NewGitRepo(t, "synthetic-trunk")
	repo.Commit("synthetic root", "root.txt", "root")
	repo.Run("branch", "synthetic-held")
	elsewhere := repo.Worktree("synthetic-held")
	other := elsewhere.Dir
	git := Client{Runner: subprocess.ExecRunner{}}

	inRepo(t, repo.Dir)
	if clean, err := git.WorktreeClean(context.Background(), other); err != nil || !clean {
		t.Fatalf("WorktreeClean() = %t, %v, want a fresh worktree clean", clean, err)
	}
	elsewhere.Write("synthetic-new.txt", "new")
	if clean, err := git.WorktreeClean(context.Background(), other); err != nil || clean {
		t.Errorf("WorktreeClean() = %t, %v, want an untracked file to count", clean, err)
	}
}

// Moving another worktree's branch strands its index; SwitchTreeIn is what
// brings it along, leaving git status there clean and its files at the new tip.
func TestSwitchTreeInBringsAnotherWorktreeAlong(t *testing.T) {
	repo := testutil.NewGitRepo(t, "synthetic-trunk")
	repo.Commit("synthetic root", "root.txt", "root")
	from := repo.Revision("HEAD")
	repo.Run("branch", "synthetic-held")
	repo.Commit("synthetic change", "root.txt", "changed")
	to := repo.Revision("HEAD")
	elsewhere := repo.Worktree("synthetic-held")
	other := elsewhere.Dir
	repo.Run("update-ref", "refs/heads/synthetic-held", to)
	if status := elsewhere.Run("status", "--porcelain"); status == "" {
		t.Fatal("moving the ref did not strand the worktree, so this proves nothing")
	}

	inRepo(t, repo.Dir)
	if err := (Client{Runner: subprocess.ExecRunner{}}).SwitchTreeIn(context.Background(), other, from, to); err != nil {
		t.Fatal(err)
	}
	if status := elsewhere.Run("status", "--porcelain"); status != "" {
		t.Errorf("status after SwitchTreeIn = %q, want clean", status)
	}
	if content, err := os.ReadFile(other + "/root.txt"); err != nil || string(content) != "changed" {
		t.Errorf("root.txt = %q, %v, want the new tip's content", content, err)
	}
}

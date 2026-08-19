package git

import (
	"context"
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

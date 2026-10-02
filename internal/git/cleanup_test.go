package git

import (
	"context"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
)

func TestDeleteBranchAtLeasesTheAssessedTip(t *testing.T) {
	repo := testutil.NewGitRepo(t, "synthetic-main")
	repo.Commit("synthetic root", "root.txt", "root")
	repo.Run("switch", "-qc", "synthetic-work")
	repo.Commit("synthetic change", "change.txt", "change")
	old := strings.TrimSpace(repo.Run("rev-parse", "HEAD"))
	repo.Commit("synthetic later change", "later.txt", "later")
	newTip := strings.TrimSpace(repo.Run("rev-parse", "HEAD"))
	repo.Run("switch", "-q", "synthetic-main")
	t.Chdir(repo.Dir)
	client := Client{Runner: subprocess.ExecRunner{}}
	if err := client.DeleteBranchAt(context.Background(), "synthetic-work", old); err == nil {
		t.Fatal("deleted a changed tip")
	}
	if got := strings.TrimSpace(repo.Run("rev-parse", "synthetic-work")); got != newTip {
		t.Fatalf("changed branch was altered: %s", got)
	}
	repo.Run("config", "branch.synthetic-work.remote", "synthetic-origin")
	if err := client.DeleteBranchAt(context.Background(), "synthetic-work", newTip); err != nil {
		t.Fatal(err)
	}
	if got := repo.Run("branch", "--list", "synthetic-work"); strings.TrimSpace(got) != "" {
		t.Fatalf("branch survived: %s", got)
	}
	if _, err := client.run(context.Background(), "config", "--get", "branch.synthetic-work.remote"); err == nil {
		t.Fatal("branch configuration survived deletion")
	}
	if got := repo.Run("status", "--porcelain"); got != "" {
		t.Fatalf("dirty tree: %s", got)
	}
}

func TestDeleteBranchAtPassesALeaseThroughTheProcessSeam(t *testing.T) {
	recorder := testutil.FakeCLIs(t, map[string][]testutil.Route{"git": {
		{Prefix: "worktree list --porcelain", Lines: []string{"worktree /synthetic/repo", "branch refs/heads/synthetic-main"}},
		{Prefix: "update-ref --no-deref -d refs/heads/synthetic-work synthetic-tip"},
		{Prefix: "config --remove-section branch.synthetic-work"},
	}})
	if err := (Client{Runner: subprocess.ExecRunner{}}).DeleteBranchAt(context.Background(), "synthetic-work", "synthetic-tip"); err != nil {
		t.Fatal(err)
	}
	recorder.Find("git update-ref --no-deref -d refs/heads/synthetic-work synthetic-tip")
	recorder.AssertNone("git branch -D", "git push", "gh ")
}

package prune

import (
	"context"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
)

// A squash merge is the commonest way a branch lands and the one a per-commit
// comparison cannot see, so this builds a real one rather than asking a fake:
// the fake answers whatever it is told, and the question is what Git makes of
// three commits combined into one.
//
// Nothing leaves the machine — no remote, and every name invented.
func TestASquashMergedBranchHasLanded(t *testing.T) {
	repo := testutil.NewGitRepo(t, "synthetic-main")
	repo.Commit("synthetic root", "root.txt", "root")

	repo.Run("checkout", "-q", "-b", "synthetic-top")
	repo.Commit("synthetic one", "one.txt", "one")
	repo.Commit("synthetic two", "two.txt", "two")
	repo.Commit("synthetic three", "three.txt", "three")

	// How the branch lands: every commit combined into one on the trunk, so
	// none of them has an equivalent there and the branch as a whole has
	// nothing left to contribute.
	repo.Run("checkout", "-q", "synthetic-main")
	repo.Run("merge", "-q", "--squash", "synthetic-top")
	repo.Run("commit", "-qm", "synthetic squash of synthetic-top")

	t.Chdir(repo.Dir)
	client := git.Client{Runner: subprocess.ExecRunner{}}
	service := Service{Git: client}
	edge := graph.Edge{Parent: "synthetic-main", ForkPoint: forkPoint(t, repo)}

	// Per commit alone, this branch reads as having three commits to give.
	absent, _, err := client.Cherry(context.Background(), edge.Parent, "synthetic-top", edge.ForkPoint)
	if err != nil {
		t.Fatalf("Cherry() error = %v", err)
	}
	if len(absent) != 3 {
		t.Fatalf("cherry found %d commits with no equivalent, want 3 — the test no longer builds a squash merge", len(absent))
	}

	landed, err := service.landed(context.Background(), "synthetic-top", edge)
	if err != nil {
		t.Fatalf("landed() error = %v", err)
	}
	if !landed {
		t.Error("a squash-merged branch was not recognised as landed, so prune would forget nothing on the commonest way a branch lands")
	}
}

// A branch with work of its own must still not be forgotten, or the whole-branch
// test would answer yes to everything.
func TestABranchWithWorkOfItsOwnHasNotLanded(t *testing.T) {
	repo := testutil.NewGitRepo(t, "synthetic-main")
	repo.Commit("synthetic root", "root.txt", "root")

	repo.Run("checkout", "-q", "-b", "synthetic-top")
	repo.Commit("synthetic one", "one.txt", "one")

	t.Chdir(repo.Dir)
	service := Service{Git: git.Client{Runner: subprocess.ExecRunner{}}}
	edge := graph.Edge{Parent: "synthetic-main", ForkPoint: forkPoint(t, repo)}

	landed, err := service.landed(context.Background(), "synthetic-top", edge)
	if err != nil {
		t.Fatalf("landed() error = %v", err)
	}
	if landed {
		t.Error("a branch with a commit of its own was reported as landed")
	}
}

func forkPoint(t *testing.T, repo testutil.GitRepo) string {
	t.Helper()
	return strings.TrimSpace(repo.Run("merge-base", "synthetic-main", "synthetic-top"))
}

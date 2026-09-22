package prune

import (
	"context"
	"slices"
	"testing"

	"github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
)

// A branch deleted with plain Git leaves its edge behind. prune asked Git
// whether it had landed, by name, and failed the whole run on the answer: the
// command for forgetting branches could not run because of one it could not
// judge. It is reported with the command that does forget a stale edge, and
// every other branch is judged as before.
func TestPruneReportsADeletedBranchRatherThanFailingOnIt(t *testing.T) {
	repo := testutil.NewGitRepo(t, "synthetic-main")
	repo.Commit("synthetic root", "root.txt", "root")
	repo.Run("switch", "-qc", "synthetic-gone")
	repo.Commit("synthetic gone", "gone.txt", "gone")
	repo.Run("switch", "-q", "synthetic-main")
	repo.Run("switch", "-qc", "synthetic-kept")
	repo.Commit("synthetic kept", "kept.txt", "kept")

	t.Chdir(repo.Dir)
	client := git.Client{Runner: subprocess.ExecRunner{}}
	graphs := graph.Service{Git: client, Store: graph.FileStore{Git: client}, Refs: client}
	ctx := context.Background()
	for _, branch := range []string{"synthetic-gone", "synthetic-kept"} {
		plan, err := graphs.PlanTrack(ctx, graph.Selection{Branch: branch}, "synthetic-main")
		if err != nil {
			t.Fatal(err)
		}
		if err := graphs.ApplyTrack(ctx, plan); err != nil {
			t.Fatal(err)
		}
	}
	repo.Run("branch", "-qD", "synthetic-gone")

	plan, err := Service{Git: client, Graph: graphs}.Plan(ctx, graph.Selection{Branch: "synthetic-main", Scope: graph.ScopeStack})
	if err != nil {
		t.Fatalf("Plan() error = %v; a deleted branch must be reported, not fail the run", err)
	}
	if !slices.Equal(plan.Missing, []string{"synthetic-gone"}) || len(plan.Landed) != 0 {
		t.Errorf("Missing = %v, Landed = %v", plan.Missing, plan.Landed)
	}
}

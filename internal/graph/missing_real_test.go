package graph_test

import (
	"context"
	"slices"
	"testing"

	"github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
)

// A branch deleted with plain Git leaves its edge behind, and nothing g2g does
// can stop that: the deletion happens where g2g is not looking. What it can do
// is read the result. Every read used to ask Git about the missing branch by
// name and fail the whole command on the answer, so one stale edge broke graph,
// status, prune and untrack for everything recorded beside it — including the
// untrack that would have removed it.
//
// A fake answers whatever it is asked, and the question is what Git says about
// a name that is no longer a ref, so this builds a real repository.
func TestADeletedTrackedBranchIsReportedAndCanBeUntracked(t *testing.T) {
	repo := testutil.NewGitRepo(t, "synthetic-main")
	repo.Commit("synthetic root", "root.txt", "root")
	repo.Run("switch", "-qc", "synthetic-a")
	repo.Commit("synthetic a", "a.txt", "a")
	repo.Run("switch", "-qc", "synthetic-b")
	repo.Commit("synthetic b", "b.txt", "b")

	t.Chdir(repo.Dir)
	client := git.Client{Runner: subprocess.ExecRunner{}}
	service := graph.Service{Git: client, Store: graph.FileStore{Git: client}, Refs: client}
	ctx := context.Background()
	for _, edge := range []struct{ branch, parent string }{{"synthetic-a", "synthetic-main"}, {"synthetic-b", "synthetic-a"}} {
		plan, err := service.PlanTrack(ctx, graph.Selection{Branch: edge.branch}, edge.parent)
		if err != nil {
			t.Fatalf("PlanTrack(%s) error = %v", edge.branch, err)
		}
		if err := service.ApplyTrack(ctx, plan); err != nil {
			t.Fatalf("ApplyTrack(%s) error = %v", edge.branch, err)
		}
	}

	repo.Run("switch", "-q", "synthetic-main")
	repo.Run("branch", "-qD", "synthetic-a")

	// graph: the whole stack still reads, and says what happened.
	discovery, err := service.Discover(ctx, graph.Selection{Branch: "synthetic-b"})
	if err != nil {
		t.Fatalf("Discover() error = %v; a deleted branch must be reported, not fail the read", err)
	}
	if got := discovery.States["synthetic-a"]; got != graph.StateBranchMissing {
		t.Errorf("synthetic-a reads as %q, want %q", got, graph.StateBranchMissing)
	}
	if got := discovery.States["synthetic-b"]; got != graph.StateParentMissing {
		t.Errorf("synthetic-b reads as %q, want %q", got, graph.StateParentMissing)
	}
	if graph.StateBranchMissing.Restackable() {
		t.Error("a branch that is not here is not something a rewrite can act on")
	}

	// untrack: the way out has to work on exactly the branch that broke it.
	plan, err := service.PlanUntrack(ctx, graph.Selection{Branch: "synthetic-a", Scope: graph.ScopeBranch})
	if err != nil {
		t.Fatalf("PlanUntrack() error = %v", err)
	}
	if !slices.Equal(plan.Removed, []string{"synthetic-a"}) {
		t.Fatalf("Removed = %v, want synthetic-a", plan.Removed)
	}
	if !slices.Equal(plan.Orphaned, []string{"synthetic-b"}) {
		t.Errorf("Orphaned = %v, want synthetic-b reported rather than reparented", plan.Orphaned)
	}
	if err := service.ApplyUntrack(ctx, plan); err != nil {
		t.Fatalf("ApplyUntrack() error = %v", err)
	}
	adopted, err := service.Store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if adopted.Tracked("synthetic-a") {
		t.Error("the stale edge is still recorded after untrack")
	}
	if parent, _ := adopted.Parent("synthetic-b"); parent != "synthetic-a" {
		t.Errorf("synthetic-b was reparented onto %q; untrack reports the children it strands and never moves them", parent)
	}
	if err := repo.Try("rev-parse", "--verify", "-q", "refs/g2g/forkpoints/synthetic-a"); err == nil {
		t.Error("the fork-point pin for the forgotten branch survived untrack")
	}
}

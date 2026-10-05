package graph_test

import (
	"context"
	"testing"

	"github.com/shhac/g2g/internal/graph"
)

// An advanced trunk must not be recorded as the start of a replay range when
// its tip is not in the branch. The old write succeeded but could never replay.
func TestTrackOnAdvancedTrunkRecordsMergeBaseAndRepairsOldRecord(t *testing.T) {
	repo, service := sameTipRepository(t)
	fork := repo.Revision("synthetic-main")
	repo.Run("switch", "synthetic-main")
	repo.Commit("synthetic trunk advanced", "advanced.txt", "advanced")
	repo.Run("switch", "synthetic-one")
	ctx := context.Background()
	selection := graph.Selection{Branch: "synthetic-one"}
	plan, err := service.PlanTrack(ctx, selection, "synthetic-main")
	if err != nil || plan.Blocked != "" {
		t.Fatalf("track: %v, %s", err, plan.Blocked)
	}
	if got := plan.Updated.Edges[selection.Branch].ForkPoint; got != fork {
		t.Fatalf("fork point = %s, want merge base %s", got, fork)
	}
	if err := service.ApplyTrack(ctx, plan); err != nil {
		t.Fatal(err)
	}
	unchanged, err := service.PlanTrack(ctx, selection, "synthetic-main")
	if err != nil || unchanged.Refreshed || !unchanged.Updated.Equal(plan.Updated) {
		t.Fatalf("retracking overwrote a valid boundary: %v, %+v", err, unchanged)
	}
	discovery, err := service.Discover(ctx, selection)
	if err != nil || discovery.States[selection.Branch] != graph.StateNeedsRestack {
		t.Fatalf("discovery = %v, %v", discovery.States, err)
	}
	// Simulate the previous version's unusable record and run its named repair.
	broken := plan.Updated.Clone()
	edge := broken.Edges[selection.Branch]
	edge.ForkPoint = repo.Revision("synthetic-main")
	broken.Edges[selection.Branch] = edge
	if err := service.Store.Save(ctx, broken); err != nil {
		t.Fatal(err)
	}
	repaired, err := service.PlanTrack(ctx, selection, "synthetic-main")
	if err != nil || repaired.Blocked != "" || !repaired.Refreshed {
		t.Fatalf("repair: %v, %+v", err, repaired)
	}
	if got := repaired.Updated.Edges[selection.Branch].ForkPoint; got != fork {
		t.Fatalf("repaired fork = %s, want %s", got, fork)
	}
	if err := service.ApplyTrack(ctx, repaired); err != nil {
		t.Fatal(err)
	}
	if status := repo.Run("status", "--porcelain"); status != "" {
		t.Fatalf("tracking dirtied checkout: %s", status)
	}
}

func TestTrackUnrelatedHistoriesRefusesBeforeWriting(t *testing.T) {
	repo, service := sameTipRepository(t)
	repo.Run("switch", "--orphan", "synthetic-unrelated")
	repo.Commit("synthetic unrelated root", "unrelated.txt", "unrelated")
	if _, err := service.PlanTrack(context.Background(), graph.Selection{}, "synthetic-main"); err == nil {
		t.Fatal("unrelated history was given a replay boundary")
	}
	g, err := service.Store.Load(context.Background())
	if err != nil || len(g.Edges) != 0 {
		t.Fatalf("refusal changed graph: %+v, %v", g, err)
	}
	if status := repo.Run("status", "--porcelain"); status != "" {
		t.Fatalf("refusal dirtied checkout: %s", status)
	}
}

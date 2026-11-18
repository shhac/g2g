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
	if err != nil || plan.Blocked() != "" {
		t.Fatalf("track: %v, %s", err, plan.Blocked())
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
	if err != nil || repaired.Blocked() != "" || !repaired.Refreshed {
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

func TestTrackOnStaleTrunkUsesKnownUpstreamAndRepairsValidOldBoundary(t *testing.T) {
	repo, service := sameTipRepository(t)
	stale := repo.Revision("synthetic-main")
	repo.Run("switch", "-qc", "synthetic-upstream", "synthetic-main")
	repo.Commit("synthetic upstream first", "trunk.txt", "first")
	repo.Commit("synthetic upstream second", "trunk.txt", "second")
	fork := repo.Revision("HEAD")
	repo.Run("remote", "add", "origin", repo.Dir)
	repo.Run("update-ref", "refs/remotes/origin/synthetic-main", fork)
	repo.Run("switch", "-qc", "synthetic-new")
	repo.Commit("synthetic feature", "feature.txt", "feature")
	ctx := context.Background()
	selection := graph.Selection{Branch: "synthetic-new"}
	plan, err := service.PlanTrack(ctx, selection, "synthetic-main")
	if err != nil || plan.Blocked() != "" {
		t.Fatalf("track: %v, %+v", err, plan)
	}
	if got := plan.Updated.Edges[selection.Branch].ForkPoint; got != fork {
		t.Fatalf("fork = %s, want %s", got, fork)
	}
	if err := service.ApplyTrack(ctx, plan); err != nil {
		t.Fatal(err)
	}
	// Old releases recorded a reachable but too-early fork. The same-parent
	// repair must advance it without ever advancing the local trunk.
	old := plan.Updated.Clone()
	edge := old.Edges[selection.Branch]
	edge.ForkPoint = stale
	old.Edges[selection.Branch] = edge
	if err := service.Store.Save(ctx, old); err != nil {
		t.Fatal(err)
	}
	repaired, err := service.PlanTrack(ctx, selection, "synthetic-main")
	if err != nil || !repaired.Refreshed || repaired.Updated.Edges[selection.Branch].ForkPoint != fork {
		t.Fatalf("repair: %v, %+v", err, repaired)
	}
	if err := service.ApplyTrack(ctx, repaired); err != nil {
		t.Fatal(err)
	}
	if repo.Revision("synthetic-main") != stale {
		t.Fatal("tracking advanced trunk")
	}
	if repo.Revision("refs/remotes/origin/synthetic-main") != fork {
		t.Fatal("tracking changed upstream ref")
	}
	if repo.Run("status", "--porcelain") != "" {
		t.Fatal("tracking dirtied checkout")
	}
}

func TestTrackKeepsFeatureParentBoundaryDespiteNewerKnownUpstream(t *testing.T) {
	repo, service := sameTipRepository(t)
	ctx := context.Background()
	parent, err := service.PlanTrack(ctx, graph.Selection{Branch: "synthetic-one"}, "synthetic-main")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ApplyTrack(ctx, parent); err != nil {
		t.Fatal(err)
	}
	fork := repo.Revision("synthetic-one")
	repo.Run("switch", "-qc", "synthetic-published-parent")
	repo.Commit("synthetic parent followup", "parent.txt", "followup")
	repo.Run("remote", "add", "origin", repo.Dir)
	repo.Run("update-ref", "refs/remotes/origin/synthetic-one", repo.Revision("HEAD"))
	repo.Run("switch", "-qc", "synthetic-child")
	repo.Commit("synthetic child", "child.txt", "child")
	plan, err := service.PlanTrack(ctx, graph.Selection{Branch: "synthetic-child"}, "synthetic-one")
	if err != nil || plan.Blocked() != "" {
		t.Fatalf("track: %v, %+v", err, plan)
	}
	if got := plan.Updated.Edges["synthetic-child"].ForkPoint; got != fork {
		t.Fatalf("feature boundary = %s, want recorded parent tip %s", got, fork)
	}
}

// A branch that merged its parent in holds the parent's tip, so its own
// commits begin there whatever the record says. Naming the recorded parent
// again answered "nothing to do" while the old fork point went on reading as
// a parent that had moved, and only untracking the branch first could move it.
func TestTrackSeatsABranchThatHoldsItsRecordedParentsTip(t *testing.T) {
	repo, service := sameTipRepository(t)
	ctx := context.Background()
	record := func(branch, parent string) graph.TrackPlan {
		t.Helper()
		plan, err := service.PlanTrack(ctx, graph.Selection{Branch: branch}, parent)
		if err != nil || plan.Blocked() != "" {
			t.Fatalf("track %s on %s: %v, %s", branch, parent, err, plan.Blocked())
		}
		return plan
	}
	if err := service.ApplyTrack(ctx, record("synthetic-one", "synthetic-main")); err != nil {
		t.Fatal(err)
	}
	repo.Run("switch", "-qc", "synthetic-child")
	repo.Commit("synthetic child", "child.txt", "child")
	if err := service.ApplyTrack(ctx, record("synthetic-child", "synthetic-one")); err != nil {
		t.Fatal(err)
	}
	old := repo.Revision("synthetic-one")
	repo.Run("switch", "-q", "synthetic-one")
	repo.Commit("synthetic parent moves", "parent.txt", "moved")

	// The parent moved and the branch does not hold it: restack has to see
	// that, so the boundary stays.
	if kept := record("synthetic-child", "synthetic-one"); kept.Refreshed || kept.Updated.Edges["synthetic-child"].ForkPoint != old {
		t.Fatalf("a moved feature parent's tip was recorded for a branch that does not hold it: %+v", kept.Updated.Edges["synthetic-child"])
	}

	repo.Run("switch", "-q", "synthetic-child")
	repo.Run("merge", "-q", "--no-edit", "synthetic-one")
	seated := record("synthetic-child", "synthetic-one")
	if got := seated.Updated.Edges["synthetic-child"].ForkPoint; !seated.Refreshed || got != repo.Revision("synthetic-one") {
		t.Fatalf("fork point = %s (refreshed %t), want the parent's tip %s", got, seated.Refreshed, repo.Revision("synthetic-one"))
	}
	if err := service.ApplyTrack(ctx, seated); err != nil {
		t.Fatal(err)
	}
	discovery, err := service.Discover(ctx, graph.Selection{Branch: "synthetic-child"})
	if err != nil || discovery.States["synthetic-child"] != graph.StateAligned {
		t.Fatalf("states = %v, %v, want synthetic-child aligned", discovery.States, err)
	}
	if again := record("synthetic-child", "synthetic-one"); again.Refreshed {
		t.Error("a boundary already at the parent's tip was recorded again")
	}
}

// A trunk that advanced after a branch was recorded on its tip is not in the
// branch. The edge still says ancestry confirmed it, which was true when it
// was written: believing that now recorded the trunk's new tip as the fork
// point of a branch that does not hold it, and the branch read as moved off
// its parent from then on.
func TestTrackKeepsABoundaryOnATrunkThatAdvancedPastIt(t *testing.T) {
	repo, service := sameTipRepository(t)
	ctx := context.Background()
	selection := graph.Selection{Branch: "synthetic-one"}
	plan, err := service.PlanTrack(ctx, selection, "synthetic-main")
	if err != nil || plan.Blocked() != "" {
		t.Fatalf("track: %v, %s", err, plan.Blocked())
	}
	if err := service.ApplyTrack(ctx, plan); err != nil {
		t.Fatal(err)
	}
	fork := repo.Revision("synthetic-main")
	repo.Run("switch", "-q", "synthetic-main")
	repo.Commit("synthetic trunk advanced", "advanced.txt", "advanced")
	repo.Run("switch", "-q", "synthetic-one")

	again, err := service.PlanTrack(ctx, selection, "synthetic-main")
	if err != nil || again.Refreshed || again.Updated.Edges[selection.Branch].ForkPoint != fork {
		t.Fatalf("retracking moved a valid boundary to a commit the branch does not hold: %v, %+v", err, again.Updated.Edges[selection.Branch])
	}
}

// A parent rewound under its child still has its tip in the child, which is
// the evidence a boundary is seated on. Seating it there would move the
// boundary back over the commits the parent dropped and record them as the
// child's own: what restack offers as --absorb, taken without being asked.
func TestTrackDoesNotMoveABoundaryBackOntoARewoundParent(t *testing.T) {
	repo, service := sameTipRepository(t)
	repo.Run("switch", "-q", "-c", "synthetic-parent", "synthetic-main")
	repo.Commit("synthetic kept", "kept.txt", "kept")
	repo.Commit("synthetic dropped", "dropped.txt", "dropped")
	repo.Run("switch", "-q", "-c", "synthetic-child")
	repo.Commit("synthetic child", "child.txt", "child")
	ctx := context.Background()
	for _, each := range [][2]string{{"synthetic-parent", "synthetic-main"}, {"synthetic-child", "synthetic-parent"}} {
		plan, err := service.PlanTrack(ctx, graph.Selection{Branch: each[0]}, each[1])
		if err != nil || plan.Blocked() != "" {
			t.Fatalf("track %s: %v, %s", each[0], err, plan.Blocked())
		}
		if err := service.ApplyTrack(ctx, plan); err != nil {
			t.Fatal(err)
		}
	}
	fork := repo.Revision("synthetic-parent")
	repo.Run("branch", "-f", "synthetic-parent", "synthetic-parent~1")

	again, err := service.PlanTrack(ctx, graph.Selection{Branch: "synthetic-child"}, "synthetic-parent")
	if err != nil || again.Refreshed || again.Updated.Edges["synthetic-child"].ForkPoint != fork {
		t.Fatalf("retracking moved the boundary back onto a rewound parent: %v, refreshed %v, %+v", err, again.Refreshed, again.Updated.Edges["synthetic-child"])
	}
}

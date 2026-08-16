package graph

import (
	"context"
	"strings"
	"testing"
)

// adoptionGit is an untracked repository: a trunk, a chain on it, a branch
// forked off the middle of that chain, and one that only shares the trunk.
//
//	synthetic-trunk
//	├─ synthetic-a
//	│  ├─ synthetic-b
//	│  └─ synthetic-side
//	└─ synthetic-elsewhere      (shares only the trunk)
func adoptionGit() fakeAncestry {
	return fakeAncestry{
		current: "synthetic-b",
		local:   []string{"synthetic-trunk", "synthetic-a", "synthetic-b", "synthetic-side", "synthetic-elsewhere"},
		ancestors: map[string][]string{
			"synthetic-b":         {"synthetic-a", "synthetic-trunk"},
			"synthetic-side":      {"synthetic-a", "synthetic-trunk"},
			"synthetic-a":         {"synthetic-trunk"},
			"synthetic-elsewhere": {"synthetic-trunk"},
			"synthetic-trunk":     {},
		},
		behind: map[string]int{
			"synthetic-a..synthetic-b":             1,
			"synthetic-trunk..synthetic-b":         2,
			"synthetic-a..synthetic-side":          1,
			"synthetic-trunk..synthetic-side":      2,
			"synthetic-trunk..synthetic-a":         1,
			"synthetic-trunk..synthetic-elsewhere": 1,
		},
	}
}

func adoptionService(t *testing.T, adopted Graph) (Service, *memoryStore) {
	t.Helper()
	return newService(t, adoptionGit(), adopted)
}

// The point of the whole feature: one command records the tree, not one edge.
func TestPlanStackRecordsTheWholeTree(t *testing.T) {
	service, store := adoptionService(t, New().WithTrunks("synthetic-trunk"))

	plan, err := service.PlanStack(context.Background(), Selection{}, "synthetic-trunk")
	if err != nil {
		t.Fatalf("PlanStack() error = %v", err)
	}
	if got, want := strings.Join(plan.Branches(), ","), "synthetic-a,synthetic-b,synthetic-side"; got != want {
		t.Fatalf("Branches() = %s, want %s", got, want)
	}
	if err := service.ApplyStack(context.Background(), plan); err != nil {
		t.Fatalf("ApplyStack() error = %v", err)
	}

	for branch, want := range map[string]string{
		"synthetic-a":    "synthetic-trunk",
		"synthetic-b":    "synthetic-a",
		"synthetic-side": "synthetic-a",
	} {
		if parent, _ := store.graph.Parent(branch); parent != want {
			t.Errorf("parent of %s = %q, want %q", branch, parent, want)
		}
	}
	// A branch that only shares the trunk is a separate stack.
	if store.graph.Tracked("synthetic-elsewhere") {
		t.Error("adoption swept in a branch that only shares the trunk")
	}
	if got := strings.Join(store.graph.Trunks, ","); got != "synthetic-trunk" {
		t.Errorf("Trunks = %s, want only the trunk", got)
	}
}

// The trunk is the one thing the user asserts, and it is inferred when only one
// recorded root is an ancestor.
func TestPlanStackInfersTheOnlyRecordedTrunk(t *testing.T) {
	service, _ := adoptionService(t, New().WithTrunks("synthetic-trunk"))

	plan, err := service.PlanStack(context.Background(), Selection{}, "")
	if err != nil {
		t.Fatalf("PlanStack() error = %v", err)
	}
	if plan.Blocked != "" {
		t.Fatalf("Blocked = %q, want the trunk inferred", plan.Blocked)
	}
	if plan.Trunk != "synthetic-trunk" {
		t.Errorf("Trunk = %q", plan.Trunk)
	}
}

// With nothing recorded there is no root to infer, and choosing one would be
// deciding where somebody's stack begins.
func TestPlanStackBlocksWhenNoTrunkCanBeInferred(t *testing.T) {
	service, _ := adoptionService(t, New())

	plan, err := service.PlanStack(context.Background(), Selection{}, "")
	if err != nil {
		t.Fatalf("PlanStack() error = %v", err)
	}
	if !strings.Contains(plan.Blocked, "--trunk") {
		t.Errorf("Blocked = %q, want it to name the flag that resolves this", plan.Blocked)
	}
	if err := service.ApplyStack(context.Background(), plan); err == nil {
		t.Error("ApplyStack() error = nil for a blocked plan")
	}
}

// Bulk adoption of all things must not quietly overwrite a deliberate choice.
func TestPlanStackBlocksOnAnEdgeRecordedDifferently(t *testing.T) {
	existing := New().WithTrunks("synthetic-trunk")
	existing, err := existing.Track("synthetic-b", Edge{Parent: "synthetic-trunk"})
	if err != nil {
		t.Fatal(err)
	}
	service, store := adoptionService(t, existing)

	plan, err := service.PlanStack(context.Background(), Selection{}, "synthetic-trunk")
	if err != nil {
		t.Fatalf("PlanStack() error = %v", err)
	}
	if !strings.Contains(plan.Blocked, "synthetic-b") {
		t.Errorf("Blocked = %q, want it to name the disagreement", plan.Blocked)
	}
	if err := service.ApplyStack(context.Background(), plan); err == nil {
		t.Error("ApplyStack() error = nil for a blocked plan")
	}
	if parent, _ := store.graph.Parent("synthetic-b"); parent != "synthetic-trunk" {
		t.Error("a blocked plan changed the graph")
	}
}

// Re-running records nothing, which is what makes it safe to reach for.
func TestPlanStackIsRepeatable(t *testing.T) {
	service, _ := adoptionService(t, New().WithTrunks("synthetic-trunk"))

	first, err := service.PlanStack(context.Background(), Selection{}, "synthetic-trunk")
	if err != nil {
		t.Fatalf("PlanStack() error = %v", err)
	}
	if err := service.ApplyStack(context.Background(), first); err != nil {
		t.Fatalf("ApplyStack() error = %v", err)
	}

	second, err := service.PlanStack(context.Background(), Selection{}, "synthetic-trunk")
	if err != nil {
		t.Fatalf("second PlanStack() error = %v", err)
	}
	if len(second.Record) != 0 {
		t.Errorf("Record = %v on a second run, want nothing", second.Branches())
	}
	if got := strings.Join(second.Already, ","); !strings.Contains(got, "synthetic-b") {
		t.Errorf("Already = %s, want the recorded edges reported", got)
	}
}

// A plan that moved between preview and apply is caught rather than acted on.
func TestRevalidateStackRefusesAChangedGraph(t *testing.T) {
	service, store := adoptionService(t, New().WithTrunks("synthetic-trunk"))

	preview, err := service.PlanStack(context.Background(), Selection{}, "synthetic-trunk")
	if err != nil {
		t.Fatalf("PlanStack() error = %v", err)
	}
	moved, err := store.graph.Track("synthetic-b", Edge{Parent: "synthetic-trunk"})
	if err != nil {
		t.Fatal(err)
	}
	store.graph = moved

	if _, err := service.RevalidateStack(context.Background(), Selection{}, "synthetic-trunk", preview); err == nil {
		t.Error("RevalidateStack() error = nil after the graph moved")
	}
}

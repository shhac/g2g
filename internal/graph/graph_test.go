package graph

import (
	"strings"
	"testing"
)

// forest is the shape every test below reasons about:
//
//	synthetic-main
//	├─ synthetic-auth
//	│  ├─ synthetic-login
//	│  └─ synthetic-session
//	└─ synthetic-billing
func forest() Graph {
	return Graph{
		Edges: map[string]Edge{
			"synthetic-auth":    {Parent: "synthetic-main", Origin: OriginUser},
			"synthetic-login":   {Parent: "synthetic-auth", Origin: OriginAncestry},
			"synthetic-session": {Parent: "synthetic-auth", Origin: OriginAncestry},
			"synthetic-billing": {Parent: "synthetic-main", Origin: OriginUser},
		},
		Trunks: []string{"synthetic-main"},
	}
}

func TestPathWalksRootToBranch(t *testing.T) {
	path, err := forest().Path("synthetic-login")
	if err != nil {
		t.Fatalf("Path() error = %v", err)
	}
	if want := "synthetic-main,synthetic-auth,synthetic-login"; strings.Join(path, ",") != want {
		t.Errorf("Path() = %v, want %s", path, want)
	}
}

func TestPathOfAnUntrackedBranchIsJustItself(t *testing.T) {
	path, err := forest().Path("synthetic-absent")
	if err != nil {
		t.Fatalf("Path() error = %v", err)
	}
	if len(path) != 1 || path[0] != "synthetic-absent" {
		t.Errorf("Path() = %v, want just the branch", path)
	}
}

// A cycle cannot be created through Track, but a hand-edited or future-schema
// file can contain one, and every walk below assumes a forest.
func TestPathReportsACycleInsteadOfLooping(t *testing.T) {
	cyclic := Graph{Edges: map[string]Edge{
		"synthetic-a": {Parent: "synthetic-b"},
		"synthetic-b": {Parent: "synthetic-a"},
	}}

	if _, err := cyclic.Path("synthetic-a"); err == nil {
		t.Fatal("Path() error = nil for a cycle")
	}
	if err := cyclic.Validate(); err == nil {
		t.Fatal("Validate() error = nil for a cycle")
	}
}

func TestSubtreeIsPreOrderWithSortedSiblings(t *testing.T) {
	got := forest().Subtree("synthetic-auth")

	want := "synthetic-auth,synthetic-login,synthetic-session"
	if strings.Join(got, ",") != want {
		t.Errorf("Subtree() = %v, want %s", got, want)
	}
}

func TestComponentStartsFromTheRootNotTheSelection(t *testing.T) {
	got, err := forest().Component("synthetic-login")
	if err != nil {
		t.Fatalf("Component() error = %v", err)
	}

	want := "synthetic-main,synthetic-auth,synthetic-login,synthetic-session,synthetic-billing"
	if strings.Join(got, ",") != want {
		t.Errorf("Component() = %v, want %s", got, want)
	}
}

func TestRootsAreTheUntrackedParents(t *testing.T) {
	got := forest().Roots()

	if len(got) != 1 || got[0] != "synthetic-main" {
		t.Errorf("Roots() = %v, want [synthetic-main]", got)
	}
}

func TestRootsFindsEveryIndependentTree(t *testing.T) {
	two := forest()
	two.Edges["synthetic-docs"] = Edge{Parent: "synthetic-release"}

	got := two.Roots()

	if want := "synthetic-main,synthetic-release"; strings.Join(got, ",") != want {
		t.Errorf("Roots() = %v, want %s", got, want)
	}
}

func TestTrackRefusesCyclesAndSelfParents(t *testing.T) {
	base := forest()

	for name, test := range map[string]struct{ branch, parent string }{
		"self parent":     {branch: "synthetic-auth", parent: "synthetic-auth"},
		"immediate cycle": {branch: "synthetic-auth", parent: "synthetic-login"},
		"distant cycle":   {branch: "synthetic-main", parent: "synthetic-login"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := base.Track(test.branch, Edge{Parent: test.parent}); err == nil {
				t.Fatalf("Track(%s, %s) error = nil", test.branch, test.parent)
			}
		})
	}
}

func TestTrackLeavesThePreviousGraphUntouched(t *testing.T) {
	base := forest()

	updated, err := base.Track("synthetic-invoice", Edge{Parent: "synthetic-billing"})
	if err != nil {
		t.Fatalf("Track() error = %v", err)
	}
	if base.Tracked("synthetic-invoice") {
		t.Error("Track() mutated the graph it was called on; a preview must not be aliased by its plan")
	}
	if !updated.Tracked("synthetic-invoice") {
		t.Error("Track() did not record the edge")
	}
}

func TestTrackReparentsAnExistingBranch(t *testing.T) {
	updated, err := forest().Track("synthetic-login", Edge{Parent: "synthetic-billing"})
	if err != nil {
		t.Fatalf("Track() error = %v", err)
	}

	if parent, _ := updated.Parent("synthetic-login"); parent != "synthetic-billing" {
		t.Errorf("parent = %q, want synthetic-billing", parent)
	}
}

// Untracking a middle branch must not invent a new edge for its children.
// Reparenting them onto the grandparent would be exactly the guess this tool
// refuses to make elsewhere.
func TestUntrackLeavesChildrenAsReportedOrphans(t *testing.T) {
	updated := forest().Untrack("synthetic-auth")

	if updated.Tracked("synthetic-auth") {
		t.Fatal("Untrack() left the edge in place")
	}
	if parent, _ := updated.Parent("synthetic-login"); parent != "synthetic-auth" {
		t.Errorf("child parent = %q, want the removed branch rather than a guess", parent)
	}
	if want := "synthetic-login,synthetic-session"; strings.Join(updated.Orphans(), ",") != want {
		t.Errorf("Orphans() = %v, want %s", updated.Orphans(), want)
	}
}

func TestOrphansIgnoresBranchesRootedOnATrunk(t *testing.T) {
	if got := forest().Orphans(); len(got) != 0 {
		t.Errorf("Orphans() = %v, want none", got)
	}
}

func TestWithTrunksSortsAndDeduplicates(t *testing.T) {
	got := New().WithTrunks("synthetic-release", "synthetic-main", "synthetic-main", "")

	if want := "synthetic-main,synthetic-release"; strings.Join(got.Trunks, ",") != want {
		t.Errorf("Trunks = %v, want %s", got.Trunks, want)
	}
}

func TestEqualComparesStructureAndTrunks(t *testing.T) {
	base := forest()

	if !base.Equal(base.Clone()) {
		t.Error("Equal() = false for a clone")
	}
	moved, err := base.Track("synthetic-login", Edge{Parent: "synthetic-billing"})
	if err != nil {
		t.Fatal(err)
	}
	if base.Equal(moved) {
		t.Error("Equal() = true after a reparent")
	}
	if base.Equal(base.WithTrunks("synthetic-other")) {
		t.Error("Equal() = true after the trunk set changed")
	}
}

func TestEqualTreatsNilAndEmptyEdgesAsTheSame(t *testing.T) {
	if !(Graph{}).Equal(New()) {
		t.Error("Equal() = false comparing a zero graph with an empty one")
	}
}

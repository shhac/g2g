package graph

import (
	"slices"
	"strings"
	"testing"
)

// reshapeFixture is a trunk with a branch that forks into two, plus a second
// stack beside it that nothing here should touch.
func reshapeFixture() Graph {
	return Graph{
		Edges: map[string]Edge{
			"synthetic-lower": {Parent: "synthetic-main", ForkPoint: "main-tip", Origin: OriginAncestry},
			"synthetic-left":  {Parent: "synthetic-lower", ForkPoint: "lower-tip", Origin: OriginAncestry},
			"synthetic-right": {Parent: "synthetic-lower", ForkPoint: "lower-older", Origin: OriginUser},
			"synthetic-other": {Parent: "synthetic-main", ForkPoint: "main-tip"},
		},
		Trunks: []string{"synthetic-main"},
	}
}

func TestRemoveRecordsTheChildrenOnWhatTheBranchSatOn(t *testing.T) {
	adopted := reshapeFixture()

	updated, children, err := adopted.Remove("synthetic-lower")
	if err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	if !slices.Equal(children, []string{"synthetic-left", "synthetic-right"}) {
		t.Errorf("children = %v", children)
	}
	if updated.Tracked("synthetic-lower") {
		t.Error("the removed branch is still recorded")
	}
	// The fork points stay where they were: they are where the removed
	// branch's work ended, which is what keeps it out of each child's replay.
	for branch, fork := range map[string]string{"synthetic-left": "lower-tip", "synthetic-right": "lower-older"} {
		edge := updated.Edges[branch]
		if edge.Parent != "synthetic-main" || edge.ForkPoint != fork {
			t.Errorf("%s = %+v, want it under synthetic-main still forking at %s", branch, edge, fork)
		}
	}
	if updated.Edges["synthetic-right"].Origin != OriginUser {
		t.Error("a child's origin was rewritten")
	}
	if !adopted.Tracked("synthetic-lower") || adopted.Edges["synthetic-left"].Parent != "synthetic-lower" {
		t.Error("Remove changed the graph it was called on")
	}
}

func TestRemoveRefusesARoot(t *testing.T) {
	for _, branch := range []string{"synthetic-main", "synthetic-unknown"} {
		if _, _, err := reshapeFixture().Remove(branch); err == nil {
			t.Errorf("Remove(%s) = nil, want a refusal: nothing records where its children would go", branch)
		}
	}
}

func TestRenameRewritesEveryRecordThatNamesTheBranch(t *testing.T) {
	updated, err := reshapeFixture().Rename("synthetic-lower", "synthetic-renamed")
	if err != nil {
		t.Fatalf("Rename() error = %v", err)
	}

	if updated.Tracked("synthetic-lower") {
		t.Error("the old name is still recorded")
	}
	if edge := updated.Edges["synthetic-renamed"]; edge.Parent != "synthetic-main" || edge.ForkPoint != "main-tip" {
		t.Errorf("the renamed edge = %+v", edge)
	}
	if !slices.Equal(updated.Children("synthetic-renamed"), []string{"synthetic-left", "synthetic-right"}) {
		t.Errorf("children of the new name = %v", updated.Children("synthetic-renamed"))
	}
	if updated.Edges["synthetic-other"].Parent != "synthetic-main" {
		t.Error("an unrelated branch moved")
	}
}

func TestRenameMovesATrunk(t *testing.T) {
	updated, err := reshapeFixture().Rename("synthetic-main", "synthetic-trunk")
	if err != nil {
		t.Fatalf("Rename() error = %v", err)
	}

	if !slices.Equal(updated.Trunks, []string{"synthetic-trunk"}) {
		t.Errorf("trunks = %v", updated.Trunks)
	}
	if !slices.Equal(updated.Children("synthetic-trunk"), []string{"synthetic-lower", "synthetic-other"}) {
		t.Errorf("children of the renamed trunk = %v", updated.Children("synthetic-trunk"))
	}
}

func TestRenameRefusesANameTheGraphAlreadyRecords(t *testing.T) {
	for _, taken := range []string{"synthetic-left", "synthetic-main", "synthetic-lower"} {
		_, err := reshapeFixture().Rename("synthetic-other", taken)
		if err == nil || !strings.Contains(err.Error(), "already records") {
			t.Errorf("Rename onto %s = %v, want a refusal", taken, err)
		}
	}
	if _, err := reshapeFixture().Rename("synthetic-other", "synthetic-other"); err == nil {
		t.Error("renaming a branch to itself = nil")
	}
}

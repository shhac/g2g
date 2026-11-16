package stack

import (
	"context"
	"github.com/shhac/g2g/internal/graphite"
	"testing"
)

func TestTrunkSurvivesANarrowSelection(t *testing.T) {
	for _, test := range []struct {
		name     string
		snapshot Snapshot
		want     string
	}{
		{name: "full stack", snapshot: Snapshot{Base: "synthetic-main", LandingTrunk: "synthetic-main", Ancestry: []string{"synthetic-main", "synthetic-parent", "synthetic-head"}}, want: "synthetic-main"},
		{name: "branch on feature", snapshot: Snapshot{Base: "synthetic-parent", LandingTrunk: "synthetic-main", Ancestry: []string{"synthetic-main", "synthetic-parent", "synthetic-head"}}, want: "synthetic-main"},
		{name: "without ancestry", snapshot: Snapshot{Base: "synthetic-main"}, want: "synthetic-main"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.snapshot.Trunk(); got != test.want {
				t.Errorf("Trunk = %q, want %q", got, test.want)
			}
		})
	}
}

func TestGraphiteTrunkRespectsDeclaredBoundary(t *testing.T) {
	parents := map[string]string{"synthetic-develop": "", "synthetic-main": "synthetic-develop", "synthetic-parent": "synthetic-main", "synthetic-feature": "synthetic-parent"}
	for _, scope := range []Scope{ScopePath, ScopeBranch, ScopeSubtree} {
		t.Run(string(scope), func(t *testing.T) {
			requested := "synthetic-main"
			roots := []string{"synthetic-main"}
			if scope == ScopePath {
				roots = append(roots, "synthetic-develop")
			} else {
				requested = ""
			}
			snapshot, err := resolveGraphiteSelection(context.Background(), parityGit{branches: []string{"synthetic-develop", "synthetic-main", "synthetic-parent", "synthetic-feature"}}, parityGraphite{forest: graphite.Forest{Parents: parents, Roots: roots}}, Selection{Branch: "synthetic-feature", Scope: scope, Trunk: requested}, "synthetic command")
			if err != nil {
				t.Fatal(err)
			}
			if got := snapshot.Trunk(); got != "synthetic-main" {
				t.Fatalf("Trunk = %s; want selected landing trunk synthetic-main (base %s, ancestry %v)", got, snapshot.Base, snapshot.Ancestry)
			}
		})
	}
}

func TestSourceTrunksSurviveFeatureSelectionBase(t *testing.T) {
	for _, scope := range []Scope{ScopeBranch, ScopeSubtree} {
		t.Run(string(scope), func(t *testing.T) {
			parents := map[string]string{"synthetic-trunk": "", "synthetic-a": "synthetic-trunk", "synthetic-b": "synthetic-a"}
			g2g, err := g2gRecord(parents, []string{"synthetic-trunk", "synthetic-a", "synthetic-b"}).Select(context.Background(), Selection{Branch: "synthetic-b", Scope: scope}, "synthetic command")
			if err != nil {
				t.Fatal(err)
			}
			selector, _ := syntheticSelector(syntheticPRs())
			github, err := selector.Select(context.Background(), Selection{Branch: "synthetic-b", Scope: scope}, "synthetic command")
			if err != nil {
				t.Fatal(err)
			}
			for _, snapshot := range []Snapshot{g2g, github} {
				if snapshot.Base != "synthetic-a" || snapshot.Trunk() != "synthetic-trunk" {
					t.Errorf("base=%s, trunk=%s", snapshot.Base, snapshot.Trunk())
				}
			}
		})
	}
}

func TestSnapshotEqualityIncludesLandingTrunk(t *testing.T) {
	snapshot := Snapshot{Base: "synthetic-parent", Ancestry: []string{"synthetic-develop", "synthetic-main", "synthetic-head"}, LandingTrunk: "synthetic-main"}
	other := snapshot
	other.LandingTrunk = "synthetic-develop"
	if snapshot.Equal(other) {
		t.Fatal("changed landing destination revalidated as equal")
	}
	if snapshot.Trunk() != "synthetic-main" {
		t.Fatalf("explicit trunk lost to ancestry: %s", snapshot.Trunk())
	}
}

func TestNarrowGraphiteSelectionDoesNotGuessAmbiguousLandingTrunk(t *testing.T) {
	parents := map[string]string{"synthetic-develop": "", "synthetic-main": "synthetic-develop", "synthetic-parent": "synthetic-main", "synthetic-feature": "synthetic-parent"}
	snapshot, err := resolveGraphiteSelection(context.Background(), parityGit{branches: []string{"synthetic-develop", "synthetic-main", "synthetic-parent", "synthetic-feature"}}, parityGraphite{forest: graphite.Forest{Parents: parents, Roots: []string{"synthetic-develop", "synthetic-main"}}}, Selection{Branch: "synthetic-feature", Scope: ScopeBranch}, "synthetic command")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Base != "synthetic-parent" || snapshot.Trunk() != "" {
		t.Fatalf("narrow selection guessed a landing trunk: %+v", snapshot)
	}
}

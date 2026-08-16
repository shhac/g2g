package graph

import (
	"context"
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/stack"
)

func selectorService(adopted Graph) Selector {
	git := fakeAncestry{
		current:   "synthetic-b",
		local:     []string{"synthetic-trunk", "synthetic-a", "synthetic-b"},
		ancestors: map[string][]string{"synthetic-a": {"synthetic-trunk"}, "synthetic-b": {"synthetic-a"}},
	}
	return Selector{Service: Service{Git: git, Store: &memoryStore{graph: adopted}}}
}

func chain() Graph {
	return Graph{
		Edges: map[string]Edge{
			"synthetic-a": {Parent: "synthetic-trunk"},
			"synthetic-b": {Parent: "synthetic-a"},
		},
		Trunks: []string{"synthetic-trunk"},
	}
}

// Adoption is the claim, so describing a branch is exactly asking whether the
// store records one.
func TestSelectorDescribesOnlyAdoptedBranches(t *testing.T) {
	selector := selectorService(chain())

	for branch, want := range map[string]bool{"synthetic-b": true, "synthetic-trunk": false, "synthetic-absent": false} {
		describes, err := selector.Describes(context.Background(), branch)
		if err != nil {
			t.Fatalf("Describes(%s) error = %v", branch, err)
		}
		if describes != want {
			t.Errorf("Describes(%s) = %t, want %t", branch, describes, want)
		}
	}
}

// The root is the base a projection sits on, and everything above it is the
// stack — the same shape a Graphite selection produces.
func TestSelectorReturnsTheRootAsTheBase(t *testing.T) {
	snapshot, err := selectorService(chain()).Select(context.Background(), stack.Selection{Branch: "synthetic-b"}, "g2g test")
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}

	if snapshot.Base != "synthetic-trunk" {
		t.Errorf("Base = %q, want the root", snapshot.Base)
	}
	if got := strings.Join(snapshot.Branches, ","); got != "synthetic-a,synthetic-b" {
		t.Errorf("Branches = %s, want the stack above the base", got)
	}
	if snapshot.Target != "synthetic-b" {
		t.Errorf("Target = %q", snapshot.Target)
	}
}

// A branch with nothing recorded under it has no base to sit on, which is the
// same refusal a Graphite path with no ancestor gets.
func TestSelectorRefusesABranchWithNoRecordedParent(t *testing.T) {
	_, err := selectorService(New()).Select(context.Background(), stack.Selection{Branch: "synthetic-b"}, "g2g test")
	if err == nil {
		t.Fatal("Select() error = nil for a branch with no recorded parent")
	}
	if !strings.Contains(err.Error(), "no recorded parent") {
		t.Errorf("error = %v", err)
	}
}

// A name readable as an option must not reach the command it is passed to,
// whichever source supplied it.
func TestSelectorRefusesOptionLikeBranchNames(t *testing.T) {
	unsafe := Graph{Edges: map[string]Edge{"-synthetic": {Parent: "synthetic-trunk"}}}
	selector := Selector{Service: Service{
		Git:   fakeAncestry{current: "-synthetic", local: []string{"synthetic-trunk", "-synthetic"}},
		Store: &memoryStore{graph: unsafe},
	}}

	_, err := selector.Select(context.Background(), stack.Selection{Branch: "-synthetic"}, "gh stack link")
	if err == nil {
		t.Fatal("Select() error = nil for an option-like branch name")
	}
	if !strings.Contains(err.Error(), "safely") {
		t.Errorf("error = %v", err)
	}
}

func TestSelectorWithoutAStoreDescribesNothing(t *testing.T) {
	describes, err := (Selector{}).Describes(context.Background(), "synthetic-b")
	if err != nil || describes {
		t.Errorf("Describes() = %t, %v; want false", describes, err)
	}
}

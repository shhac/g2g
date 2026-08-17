// Tests for the untrack command, which removes an edge and reports what it
// strands rather than reparenting.
package cli

import (
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/graph"
)

// Removing a middle branch must show what it strands rather than reparenting.
func TestUntrackReportsTheChildrenItStrands(t *testing.T) {
	out, store, err := runGraph(t, graphFixture(), false, "untrack", "--branch", "synthetic-auth")
	if err != nil {
		t.Fatalf("untrack: %v\n%s", err, out)
	}
	if !strings.Contains(out, "without a tracked parent") {
		t.Errorf("output does not report the orphans:\n%s", out)
	}
	if !strings.Contains(out, "not reparented") {
		t.Errorf("output does not say the children keep their parent:\n%s", out)
	}
	if store.writes != 0 {
		t.Error("a preview wrote to the store")
	}
	assertGolden(t, "untrack-orphans-plain", out)
}

func TestUntrackSubtreeRemovesDescendants(t *testing.T) {
	out, store, err := runGraph(t, graphFixture(), false, "untrack", "--branch", "synthetic-auth", "--scope", "subtree", "--apply")
	if err != nil {
		t.Fatalf("untrack --apply: %v\n%s", err, out)
	}
	if store.writes != 1 {
		t.Fatalf("store writes = %d, want exactly one", store.writes)
	}
	for _, branch := range []string{"synthetic-auth", "synthetic-login", "synthetic-session"} {
		if store.graph.Tracked(branch) {
			t.Errorf("%s is still tracked", branch)
		}
	}
	if !store.graph.Tracked("synthetic-billing") {
		t.Error("untrack --scope subtree removed a branch outside the subtree")
	}
}

func TestUntrackOfAnUntrackedBranchIsANoOp(t *testing.T) {
	out, store, err := runGraph(t, graph.New(), false, "untrack", "--branch", "synthetic-login", "--apply")
	if err != nil {
		t.Fatalf("untrack --apply: %v\n%s", err, out)
	}
	if store.writes != 0 {
		t.Errorf("store writes = %d, want none", store.writes)
	}
	if !strings.Contains(out, "Nothing to do") {
		t.Errorf("output does not report the no-op:\n%s", out)
	}
}

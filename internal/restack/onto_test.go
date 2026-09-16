package restack

import (
	"context"
	"testing"

	"github.com/shhac/g2g/internal/graph"
)

// --onto had no test that reparented a branch sitting under an ordinary
// tracked branch. Both cases that did exist reparented one sitting under a
// trunk, which is the single shape that hides what these two check: trunks
// carry no edge, so "is the recorded parent tracked anywhere" happened to
// answer the question "is this the selection's root" correctly, and only
// there.

// ontoStack is a chain under one trunk, with a second trunk to move onto.
func ontoStack() graph.Graph {
	return graph.Graph{
		Edges: map[string]graph.Edge{
			"synthetic-a": {Parent: "synthetic-trunk", ForkPoint: "trunk-old"},
			"synthetic-b": {Parent: "synthetic-a", ForkPoint: "a-old"},
			"synthetic-c": {Parent: "synthetic-b", ForkPoint: "b-old"},
		},
		Trunks: []string{"synthetic-trunk", "synthetic-release"},
	}
}

func ontoGit() *fakeGit {
	return &fakeGit{
		current: "synthetic-c",
		local:   []string{"synthetic-trunk", "synthetic-release", "synthetic-a", "synthetic-b", "synthetic-c"},
		objects: map[string]string{
			"synthetic-trunk":   "trunk-old",
			"synthetic-release": "release-tip",
			"synthetic-a":       "a-old",
			"synthetic-b":       "b-old",
			"synthetic-c":       "c-old",
			"trunk-old":         "trunk-old",
			"a-old":             "a-old",
			"b-old":             "b-old",
			"release-tip":       "release-tip",
		},
		ancestors: map[string][]string{
			"synthetic-a": {"trunk-old"},
			"synthetic-b": {"a-old", "trunk-old"},
			"synthetic-c": {"b-old", "a-old", "trunk-old"},
		},
		replaySupported: true,
		previewClean:    true,
	}
}

func ontoSelection() graph.Selection {
	return graph.Selection{Branch: "synthetic-b", Scope: graph.ScopeSubtree}
}

// A branch selected as a subtree has an ordinary tracked branch for a parent,
// so asking the store whether that parent is tracked answers "yes" and leaves
// the recorded parent in place. The branch then sits exactly where its fork
// point says, produces no step, and Apply returns having rewritten nothing —
// while reporting success.
func TestOntoMovesABranchWhoseParentIsATrackedBranch(t *testing.T) {
	service, _, _ := newService(ontoGit(), ontoStack())

	plan, err := service.Plan(context.Background(), ontoSelection(), ToBranch("synthetic-release"), false)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	if len(plan.Steps) == 0 {
		t.Fatal("Plan() produced no steps, so --onto would rewrite nothing at all")
	}
	step := plan.Steps[0]
	if step.Branch != "synthetic-b" {
		t.Fatalf("first step is %q, want the selection's root", step.Branch)
	}
	if step.Parent != "synthetic-release" {
		t.Errorf("step parent = %q, want the --onto target", step.Parent)
	}
	if step.ForkPoint != "a-old" {
		t.Errorf("fork point = %q, want the branch's own recorded fork point", step.ForkPoint)
	}
}

// Everything above the root is rewritten because its parent is, not because
// its parent changed. Recording the --onto target for all of them turns a
// chain into a fan, and the fork point refreshed alongside then widens each
// child's replay range to swallow the commits of the branch it is stacked on.
func TestOntoReparentsOnlyTheSelectionRoot(t *testing.T) {
	service, store, _ := newService(ontoGit(), ontoStack())

	plan, err := service.Plan(context.Background(), ontoSelection(), ToBranch("synthetic-release"), false)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	moves := plan.reparenting()
	if len(moves) != 1 || moves["synthetic-b"] != "synthetic-release" {
		t.Fatalf("reparenting() = %v, want only the root moved", moves)
	}

	if err := service.Apply(context.Background(), plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if parent := store.graph.Edges["synthetic-b"].Parent; parent != "synthetic-release" {
		t.Errorf("synthetic-b parent = %q, want the --onto target", parent)
	}
	if parent := store.graph.Edges["synthetic-c"].Parent; parent != "synthetic-b" {
		t.Errorf("synthetic-c parent = %q, want the branch it is stacked on, unchanged", parent)
	}
}

// The fork point is the bottom of a replay range, so a child recorded against
// the --onto target starts its next replay below its own parent's work and
// offers those commits to the engine a second time.
func TestOntoLeavesAChildsForkPointOnItsOwnParent(t *testing.T) {
	git := ontoGit()
	service, store, _ := newService(git, ontoStack())

	plan, err := service.Plan(context.Background(), ontoSelection(), ToBranch("synthetic-release"), false)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if err := service.Apply(context.Background(), plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	tip, err := git.Resolve(context.Background(), "synthetic-b")
	if err != nil {
		t.Fatal(err)
	}
	if fork := store.graph.Edges["synthetic-c"].ForkPoint; fork != tip {
		t.Errorf("synthetic-c fork point = %q, want synthetic-b's tip %q", fork, tip)
	}
}

// A rewrite with nothing to replay still has an edge to record: the branch
// already sits on the target and only the graph disagrees. Returning early on
// an empty step list made --onto silently do nothing in exactly the case it
// was easiest to reach by hand.
func TestOntoRecordsTheEdgeWhenThereIsNothingToReplay(t *testing.T) {
	git := ontoGit()
	// b already sits on the release tip, so there is no work to move.
	git.objects["synthetic-b"] = "release-tip"
	git.ancestors["synthetic-b"] = []string{"release-tip"}
	adopted := ontoStack()
	adopted.Edges["synthetic-b"] = graph.Edge{Parent: "synthetic-a", ForkPoint: "release-tip"}
	delete(adopted.Edges, "synthetic-c")
	service, store, _ := newService(git, adopted)

	plan, err := service.Plan(context.Background(), ontoSelection(), ToBranch("synthetic-release"), false)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if len(plan.Steps) != 0 {
		t.Fatalf("Steps = %v, want nothing to replay for this case", plan.Steps)
	}

	if err := service.Apply(context.Background(), plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if parent := store.graph.Edges["synthetic-b"].Parent; parent != "synthetic-release" {
		t.Errorf("synthetic-b parent = %q, want the --onto target recorded anyway", parent)
	}
}

// A path selection includes the trunk, which carries no edge. The root is the
// branch above it, so --onto moves that one and leaves the chain above intact.
func TestOntoOnAPathSelectionMovesTheBranchAboveTheTrunk(t *testing.T) {
	service, _, _ := newService(ontoGit(), ontoStack())

	plan, err := service.Plan(context.Background(), graph.Selection{Branch: "synthetic-c", Scope: graph.ScopePath}, ToBranch("synthetic-release"), false)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	moves := plan.reparenting()
	if len(moves) != 1 || moves["synthetic-a"] != "synthetic-release" {
		t.Fatalf("reparenting() = %v, want only synthetic-a moved", moves)
	}
}

package prune

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/graph"
)

// pruneGit answers by content: a branch listed in landed has nothing its parent
// does not already carry.
type pruneGit struct {
	landed map[string]bool
	err    error
	asked  []string
}

func (g *pruneGit) Cherry(_ context.Context, upstream, head, _ string) (absent, present []string, err error) {
	if g.err != nil {
		return nil, nil, g.err
	}
	g.asked = append(g.asked, upstream+".."+head)
	if g.landed[head] {
		return nil, []string{"synthetic-commit"}, nil
	}
	return []string{"synthetic-commit"}, nil, nil
}

type pruneAncestry struct{ current string }

func (a pruneAncestry) CurrentBranch(context.Context) (string, error) { return a.current, nil }
func (pruneAncestry) LocalBranches(context.Context) ([]string, error) {
	return []string{"synthetic-trunk", "synthetic-a", "synthetic-b", "synthetic-c"}, nil
}
func (pruneAncestry) AncestorBranches(context.Context, string) ([]string, error) { return nil, nil }
func (pruneAncestry) Divergence(context.Context, string, string) (int, int, error) {
	return 0, 0, nil
}
func (pruneAncestry) IsAncestor(context.Context, string, string) (bool, error) { return true, nil }
func (pruneAncestry) Resolve(_ context.Context, ref string) (string, error)    { return ref, nil }

type pruneStore struct {
	graph  graph.Graph
	writes int
}

func (s *pruneStore) Load(context.Context) (graph.Graph, error) { return s.graph, nil }
func (s *pruneStore) Save(_ context.Context, updated graph.Graph) error {
	s.graph = updated
	s.writes++
	return nil
}
func (*pruneStore) Path(context.Context) (string, error) {
	return "/synthetic/repo/.git/g2g/graph.json", nil
}

// pruneRefs records what was unpinned. The real one deletes a ref under
// refs/g2g/forkpoints, which is the part sync never exercised.
type pruneRefs struct {
	unpinned []string
	err      error
}

func (r *pruneRefs) PinForkPoint(context.Context, string, string) error { return nil }
func (r *pruneRefs) UnpinForkPoint(_ context.Context, branch string) error {
	if r.err != nil {
		return r.err
	}
	r.unpinned = append(r.unpinned, branch)
	return nil
}

// syntheticStack is trunk → a → b → c, all recorded.
func syntheticService(t *testing.T, current string, landed ...string) (Service, *pruneStore, *pruneRefs, *pruneGit) {
	t.Helper()

	recorded := graph.New()
	for _, edge := range []struct{ branch, parent string }{
		{"synthetic-a", "synthetic-trunk"},
		{"synthetic-b", "synthetic-a"},
		{"synthetic-c", "synthetic-b"},
	} {
		updated, err := recorded.Track(edge.branch, graph.Edge{Parent: edge.parent, ForkPoint: "0000000000000000000000000000000000000000"})
		if err != nil {
			t.Fatalf("Track(%q) error = %v", edge.branch, err)
		}
		recorded = updated
	}
	store := &pruneStore{graph: recorded}
	refs := &pruneRefs{}
	git := &pruneGit{landed: map[string]bool{}}
	for _, branch := range landed {
		git.landed[branch] = true
	}
	return Service{
		Git:   git,
		Graph: graph.Service{Git: pruneAncestry{current: current}, Store: store, Refs: refs},
	}, store, refs, git
}

// A branch whose own commits all have an equivalent in its parent has landed,
// whether it was merged, squashed, or rebased there by somebody else.
func TestPlanNamesOnlyTheBranchesThatHaveLanded(t *testing.T) {
	service, _, _, git := syntheticService(t, "synthetic-c", "synthetic-a")

	plan, err := service.Plan(context.Background(), graph.Selection{Branch: "synthetic-c", Scope: graph.ScopeStack})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if got, want := strings.Join(plan.Landed, ","), "synthetic-a"; got != want {
		t.Errorf("landed = %q, want %q", got, want)
	}
	// The trunk is not a branch with work to land, so it is never asked about.
	for _, asked := range git.asked {
		if strings.HasSuffix(asked, "..synthetic-trunk") {
			t.Errorf("prune asked whether the trunk had landed: %q", asked)
		}
	}
}

// This is the path sync never executed: its tests built a graph service with no
// ref writer, so the unpin returned early every time. A fork point that outlives
// its edge keeps objects reachable that nothing refers to any more.
func TestApplyForgetsTheBranchAndReleasesItsForkPoint(t *testing.T) {
	// The tip has landed and nothing is recorded under it, so forgetting it
	// strands nobody. That is the ordinary case: work lands from the top.
	service, store, refs, _ := syntheticService(t, "synthetic-c", "synthetic-c")

	plan, err := service.Plan(context.Background(), graph.Selection{Branch: "synthetic-c", Scope: graph.ScopeStack})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.Blocked != "" {
		t.Fatalf("Blocked = %q; forgetting a tip strands nothing", plan.Blocked)
	}
	if err := service.Apply(context.Background(), plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if store.graph.Tracked("synthetic-c") {
		t.Error("the landed branch is still recorded")
	}
	if !store.graph.Tracked("synthetic-b") {
		t.Error("prune forgot a branch that had not landed")
	}
	if got, want := strings.Join(refs.unpinned, ","), "synthetic-c"; got != want {
		t.Errorf("unpinned = %q, want %q", got, want)
	}
}

// Forgetting a parent while keeping its child would strand the child. This
// reports rather than reparents, which is the rule untrack follows.
func TestPlanRefusesToStrandABranchRecordedUnderALandedOne(t *testing.T) {
	service, _, _, _ := syntheticService(t, "synthetic-c", "synthetic-a")

	plan, err := service.Plan(context.Background(), graph.Selection{Branch: "synthetic-a", Scope: graph.ScopeBranch})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.Blocked == "" {
		t.Fatal("Blocked = \"\"; forgetting synthetic-a alone would strand synthetic-b")
	}
	if !strings.Contains(plan.Blocked, "strand") {
		t.Errorf("refusal does not say what it protects: %q", plan.Blocked)
	}
}

// A blocked plan must not write, even if a caller asks it to.
func TestApplyRefusesABlockedPlan(t *testing.T) {
	service, store, refs, _ := syntheticService(t, "synthetic-c", "synthetic-a")

	plan, err := service.Plan(context.Background(), graph.Selection{Branch: "synthetic-a", Scope: graph.ScopeBranch})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if err := service.Apply(context.Background(), plan); err == nil {
		t.Fatal("Apply() error = nil for a blocked plan")
	}
	if store.writes != 0 || len(refs.unpinned) != 0 {
		t.Errorf("a blocked plan wrote: %d graph writes, %v unpinned", store.writes, refs.unpinned)
	}
}

// Nothing landed is a valid answer, not an error, and it writes nothing.
func TestNothingLandedWritesNothing(t *testing.T) {
	service, store, refs, _ := syntheticService(t, "synthetic-c")

	plan, err := service.Plan(context.Background(), graph.Selection{Branch: "synthetic-c", Scope: graph.ScopeStack})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if !plan.Nothing() {
		t.Fatalf("Nothing() = false, landed = %v", plan.Landed)
	}
	if err := service.Apply(context.Background(), plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if store.writes != 0 || len(refs.unpinned) != 0 {
		t.Errorf("an empty prune wrote: %d graph writes, %v unpinned", store.writes, refs.unpinned)
	}
}

// A failed unpin is reported rather than swallowed: the graph has already been
// written at that point, so silence would leave a pin nothing will release.
func TestApplyReportsAFailedUnpin(t *testing.T) {
	service, _, refs, _ := syntheticService(t, "synthetic-c", "synthetic-c")
	refs.err = fmt.Errorf("synthetic ref failure")

	plan, err := service.Plan(context.Background(), graph.Selection{Branch: "synthetic-c", Scope: graph.ScopeStack})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if err := service.Apply(context.Background(), plan); err == nil {
		t.Fatal("Apply() error = nil when releasing a fork point failed")
	}
}

// Cherry reports every commit as absent from the trunk, so nothing here reads
// as landed by content unless a case is about that.
func (a pruneAncestry) Cherry(_ context.Context, _, head, _ string) (absent, present []string, err error) {
	return []string{head + "-own-commit"}, nil, nil
}

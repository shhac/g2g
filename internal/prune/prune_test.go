package prune

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/testutil"
)

// pruneGit answers by content: a branch listed in landed has nothing its parent
// does not already carry.
//
// squashed is the other way a branch lands, and the one Cherry cannot see: its
// commits have no individual equivalent in the parent, and merging the whole
// branch in changes the parent not at all.
type pruneGit struct {
	landed   map[string]bool
	squashed map[string]bool
	err      error
	asked    []string
	merged   []string
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

func (g *pruneGit) Absorbed(_ context.Context, base, branch string) (bool, error) {
	if g.err != nil {
		return false, g.err
	}
	g.merged = append(g.merged, base+"+"+branch)
	return g.squashed[branch], nil
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

// The way out of that refusal names each child and the branch it belongs on,
// which is the nearest one below it that is not being forgotten. Widening the
// selection is offered only for a child the selection did not ask about: one
// it did ask about survives because it has work of its own, and widening
// brings it straight back to the same refusal.
func TestTheWayOutOfAStrandNamesWhereEachChildBelongs(t *testing.T) {
	for name, test := range map[string]struct {
		selection graph.Selection
		landed    []string
		want      []string
		widen     bool
	}{
		"child selected":     {graph.Selection{Branch: "synthetic-c", Scope: graph.ScopeStack}, []string{"synthetic-a"}, []string{"g2g track --branch synthetic-b --parent synthetic-trunk"}, false},
		"landed chain":       {graph.Selection{Branch: "synthetic-c", Scope: graph.ScopeStack}, []string{"synthetic-a", "synthetic-b"}, []string{"g2g track --branch synthetic-c --parent synthetic-trunk"}, false},
		"child not selected": {graph.Selection{Branch: "synthetic-a", Scope: graph.ScopeBranch}, []string{"synthetic-a"}, []string{"g2g track --branch synthetic-b --parent synthetic-trunk"}, true},
	} {
		t.Run(name, func(t *testing.T) {
			service, _, _, _ := syntheticService(t, "synthetic-c", test.landed...)

			plan, err := service.Plan(context.Background(), test.selection)
			if err != nil {
				t.Fatalf("Plan() error = %v", err)
			}
			commands := make([]string, 0)
			widen := false
			for _, way := range plan.Repair.Ways {
				if strings.HasPrefix(way.Command, "g2g track") {
					commands = append(commands, way.Command)
				}
				widen = widen || strings.Contains(way.Effect, "widen")
			}
			if strings.Join(commands, "\n") != strings.Join(test.want, "\n") {
				t.Errorf("ways = %v, want %v", commands, test.want)
			}
			if widen != test.widen {
				t.Errorf("widening offered = %t, want %t: %+v", widen, test.widen, plan.Repair.Ways)
			}
		})
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
	return testutil.OwnCommits(head), nil, nil
}

// Absorbed answers of a whole branch what Cherry answers per commit, which is
// what a squash merge needs. Nothing here is absorbed unless a case says so.
func (a pruneAncestry) Absorbed(context.Context, string, string) (bool, error) { return false, nil }

// prune was the one mutating service with no revalidation refusal test, and it
// is what bounds Apply: Apply re-loads the graph and untracks whatever
// plan.Landed names against whatever is there now, then unpins fork points.
func TestRevalidateRefusesWhenWhatHasLandedChangedUnderneath(t *testing.T) {
	service, _, _, git := syntheticService(t, "synthetic-c", "synthetic-a")
	selection := graph.Selection{Branch: "synthetic-c", Scope: graph.ScopeStack}

	preview, err := service.Plan(context.Background(), selection)
	if err != nil {
		t.Fatal(err)
	}

	// Another branch lands underneath, so the plan is no longer the one
	// previewed and what an apply would forget has changed.
	git.landed["synthetic-b"] = true

	if _, err := service.Revalidate(context.Background(), selection, preview); err == nil {
		t.Fatal("Revalidate() error = nil, want a refusal")
	} else if !strings.Contains(err.Error(), "changed during revalidation") {
		t.Errorf("Revalidate() error = %v, want it to name the change", err)
	}
}

package graph

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// What a whole-stack adoption costs in Git calls.
//
// Not a benchmark: a ceiling. The work was quadratic in the number of local
// branches and nothing said so, because every test that exercised it used four
// branches, where quadratic and linear are the same shape. On a real
// thirty-seven branch repository it made 304 calls and took a minute and a
// half, and a preview that has to be waited out is one nobody runs.

// countingAncestry wraps a fake and records what was asked of Git.
//
// The counters are guarded because this is also the thing that would notice if
// the discovery it wraps were ever made concurrent: a fake that races is a test
// that fails for a reason that has nothing to do with the code.
type countingAncestry struct {
	Ancestry
	mu          sync.Mutex
	divergence  int
	ancestors   int
	localBranch int
}

func (c *countingAncestry) Divergence(ctx context.Context, other, target string) (int, int, error) {
	c.mu.Lock()
	c.divergence++
	c.mu.Unlock()
	return c.Ancestry.Divergence(ctx, other, target)
}

func (c *countingAncestry) AncestorBranches(ctx context.Context, target string) ([]string, error) {
	c.mu.Lock()
	c.ancestors++
	c.mu.Unlock()
	return c.Ancestry.AncestorBranches(ctx, target)
}

func (c *countingAncestry) LocalBranches(ctx context.Context) ([]string, error) {
	c.mu.Lock()
	c.localBranch++
	c.mu.Unlock()
	return c.Ancestry.LocalBranches(ctx)
}

// wideRepository is a stack of four on a trunk, plus many branches that sit on
// the trunk and belong to nobody's stack — the ordinary shape of a repository
// somebody has been working in for a while, and the shape that made the cost
// visible.
func wideRepository(unrelated int) fakeAncestry {
	local := []string{"synthetic-trunk", "synthetic-a", "synthetic-b", "synthetic-c", "synthetic-d"}
	ancestors := map[string][]string{
		"synthetic-a": {"synthetic-trunk"},
		"synthetic-b": {"synthetic-trunk", "synthetic-a"},
		"synthetic-c": {"synthetic-trunk", "synthetic-a", "synthetic-b"},
		"synthetic-d": {"synthetic-trunk", "synthetic-a", "synthetic-b", "synthetic-c"},
	}
	// Distances down the spine, so the chain has an order rather than a tie.
	behind := map[string]int{}
	spine := []string{"synthetic-trunk", "synthetic-a", "synthetic-b", "synthetic-c", "synthetic-d"}
	for below := range spine {
		for above := below + 1; above < len(spine); above++ {
			behind[spine[below]+".."+spine[above]] = above - below
		}
	}
	// Branches whose work is already in the trunk: the ordinary sediment of a
	// long-lived repository, and the shape that made this quadratic. Each has
	// no ancestor of its own, and the trunk is a descendant of it rather than
	// a candidate parent — so the preferred set comes back empty and the old
	// reading fell through to measuring every local branch against it.
	merged := make([]string, 0, unrelated)
	for index := range unrelated {
		branch := fmt.Sprintf("synthetic-other-%02d", index)
		local = append(local, branch)
		merged = append(merged, branch)
		ancestors[branch] = nil
	}
	ancestors["synthetic-trunk"] = merged
	return fakeAncestry{current: "synthetic-d", local: local, ancestors: ancestors, behind: behind}
}

func trackedTrunk() Graph {
	return Graph{Edges: map[string]Edge{}, Trunks: []string{"synthetic-trunk"}}
}

// The cost has to grow with the number of branches, not with its square.
//
// attach acts only on candidates that are genuinely ancestors, and the
// fallback Candidates keeps for the single-branch case can only produce
// branches that are not one. Asking for it measured the whole repository
// against itself and discarded the answer.
func TestAdoptingAStackDoesNotMeasureEveryBranchAgainstEveryOther(t *testing.T) {
	measured := map[int]int{}
	for _, unrelated := range []int{10, 40} {
		counter := &countingAncestry{Ancestry: wideRepository(unrelated)}
		service := Service{Git: counter, Store: &countingStore{graph: trackedTrunk()}}

		plan, err := service.PlanStack(context.Background(), Selection{Branch: "synthetic-d"}, "synthetic-trunk")
		if err != nil {
			t.Fatalf("PlanStack() error = %v", err)
		}
		if plan.Blocked != "" {
			t.Fatalf("PlanStack() blocked: %s", plan.Blocked)
		}
		// The answer must not change with the noise around it.
		if got := strings.Join(plan.Branches(), ","); got != "synthetic-a,synthetic-b,synthetic-c,synthetic-d" {
			t.Fatalf("Branches() = %s, want the stack alone", got)
		}
		measured[unrelated] = counter.divergence
	}

	// The slope, not the ratio: a fixed cost makes the ratio look superlinear
	// when the growth is perfectly flat, and it is the growth that matters.
	// Each extra branch should cost a call or two — measuring it against the
	// one thing it sits on. Under the old reading it cost one per branch
	// already in the repository, so this number grew with it.
	perBranch := float64(measured[40]-measured[10]) / 30
	if perBranch > 2 {
		t.Errorf("each extra branch costs %.1f divergence calls (%d → %d for 10 → 40), which is not linear",
			perBranch, measured[10], measured[40])
	}
	// And an absolute ceiling, because a slope measured over two points can be
	// flat while the constant is absurd. Before this, forty unrelated branches
	// cost upwards of eighteen hundred calls.
	if measured[40] > 4*40 {
		t.Errorf("forty unrelated branches cost %d divergence calls", measured[40])
	}
}

// The local branch list cannot change while one command runs, and a whole-stack
// adoption asks about every branch in the repository, so reading it once per
// branch was a process spawn per branch for an answer already in hand.
func TestAdoptingAStackReadsTheLocalBranchesOnce(t *testing.T) {
	counter := &countingAncestry{Ancestry: wideRepository(30)}
	service := Service{Git: counter, Store: &countingStore{graph: trackedTrunk()}}

	if _, err := service.PlanStack(context.Background(), Selection{Branch: "synthetic-d"}, "synthetic-trunk"); err != nil {
		t.Fatal(err)
	}

	// Discovery asks, and the adoption asks. Not once per branch.
	if counter.localBranch > 4 {
		t.Errorf("read the local branches %d times for a 35-branch repository", counter.localBranch)
	}
}

// related is what a caller acting on ancestry wants, and it is the whole of the
// answer: the set it measures already contains every ancestor there is, so the
// fallback cannot add one.
func TestRelatedFindsEveryAncestorCandidatesWould(t *testing.T) {
	git := wideRepository(6)
	roots := []string{"synthetic-trunk"}

	found, err := related(context.Background(), git, "synthetic-d", roots)
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := Candidates(context.Background(), git, "synthetic-d", roots)
	if err != nil {
		t.Fatal(err)
	}

	if ancestorNames(found) != ancestorNames(candidates) {
		t.Errorf("related found %q, Candidates found %q", ancestorNames(found), ancestorNames(candidates))
	}
}

func ancestorNames(candidates []Candidate) string {
	names := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Ancestor {
			names = append(names, candidate.Branch)
		}
	}
	return strings.Join(names, ",")
}

// countingStore is a store that needs no file.
type countingStore struct{ graph Graph }

func (s *countingStore) Load(context.Context) (Graph, error) { return s.graph.Clone(), nil }
func (s *countingStore) Save(_ context.Context, g Graph) error {
	s.graph = g.Clone()
	return nil
}
func (s *countingStore) Path(context.Context) (string, error) { return "/synthetic/graph.json", nil }

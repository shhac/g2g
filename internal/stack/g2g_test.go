package stack

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/testutil"
)

func selectorService(adopted graph.Graph) G2GSelector {
	git := g2gAncestry{
		current:   "synthetic-b",
		local:     []string{"synthetic-trunk", "synthetic-a", "synthetic-b"},
		ancestors: map[string][]string{"synthetic-a": {"synthetic-trunk"}, "synthetic-b": {"synthetic-a"}},
	}
	return G2GSelector{Service: graph.Service{Git: git, Store: &g2gStore{graph: adopted}}}
}

func chain() graph.Graph {
	return graph.Graph{
		Edges: map[string]graph.Edge{
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
	snapshot, err := selectorService(chain()).Select(context.Background(), Selection{Branch: "synthetic-b"}, "g2g test")
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
	_, err := selectorService(graph.New()).Select(context.Background(), Selection{Branch: "synthetic-b"}, "g2g test")
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
	unsafe := graph.Graph{Edges: map[string]graph.Edge{"-synthetic": {Parent: "synthetic-trunk"}}}
	selector := G2GSelector{Service: graph.Service{
		Git:   g2gAncestry{current: "-synthetic", local: []string{"synthetic-trunk", "-synthetic"}},
		Store: &g2gStore{graph: unsafe},
	}}

	_, err := selector.Select(context.Background(), Selection{Branch: "-synthetic"}, "gh stack link")
	if err == nil {
		t.Fatal("Select() error = nil for an option-like branch name")
	}
	if !strings.Contains(err.Error(), "safely") {
		t.Errorf("error = %v", err)
	}
}

// A recorded path has one root, so --trunk can only confirm the base. Naming
// anything else has to be refused: silently ignoring it would push the stack at
// a different base than the one the user asked for.
func TestSelectorAcceptsTheRootAsTrunkAndRefusesAnyOther(t *testing.T) {
	selector := selectorService(chain())

	snapshot, err := selector.Select(context.Background(), Selection{Branch: "synthetic-b", Trunk: "synthetic-trunk"}, "g2g test")
	if err != nil {
		t.Fatalf("Select() with the root as --trunk error = %v", err)
	}
	if snapshot.BaseSource != "--trunk" {
		t.Errorf("BaseSource = %q, want the flag to be credited", snapshot.BaseSource)
	}

	_, err = selector.Select(context.Background(), Selection{Branch: "synthetic-b", Trunk: "synthetic-a"}, "g2g test")
	if err == nil {
		t.Fatal("Select() error = nil for a --trunk that is not the recorded base")
	}
	if !strings.Contains(err.Error(), "synthetic-trunk") {
		t.Errorf("error = %v, want it to name the base it does have", err)
	}
}

// Completion answers from the store alone: no ancestry probing, no subprocess,
// and roots left out because a command cannot act on one.
func TestG2GCandidatesOfferWhatSelectionWouldAccept(t *testing.T) {
	candidates := G2GCandidates{Service: graph.Service{Store: &g2gStore{graph: chain()}}}

	branches, err := candidates.Branches(context.Background())
	if err != nil {
		t.Fatalf("Branches() error = %v", err)
	}
	if got := strings.Join(branches, ","); got != "synthetic-a,synthetic-b" {
		t.Errorf("Branches() = %s, want the adopted branches without the root", got)
	}

	trunks, err := candidates.Trunks(context.Background(), "synthetic-b")
	if err != nil {
		t.Fatalf("Trunks() error = %v", err)
	}
	if got := strings.Join(trunks, ","); got != "synthetic-trunk" {
		t.Errorf("Trunks() = %s, want the base of the recorded path", got)
	}

	untracked, err := candidates.Trunks(context.Background(), "synthetic-absent")
	if err != nil || len(untracked) != 0 {
		t.Errorf("Trunks(untracked) = %v, %v; want no candidates and no error", untracked, err)
	}
}

func TestSelectorWithoutAStoreDescribesNothing(t *testing.T) {
	describes, err := (G2GSelector{}).Describes(context.Background(), "synthetic-b")
	if err != nil || describes {
		t.Errorf("Describes() = %t, %v; want false", describes, err)
	}
}

// A zero value must answer rather than panic: completion reaches these on a
// keystroke, in whatever state the process happens to be wired.
func TestZeroValueG2GCandidatesOfferNothing(t *testing.T) {
	candidates := G2GCandidates{}

	branches, err := candidates.Branches(context.Background())
	if err != nil || len(branches) != 0 {
		t.Errorf("Branches() = %v, %v; want none and no error", branches, err)
	}
	trunks, err := candidates.Trunks(context.Background(), "synthetic-b")
	if err != nil || len(trunks) != 0 {
		t.Errorf("Trunks() = %v, %v; want none and no error", trunks, err)
	}
}

// An unreadable store is reported, never quietly treated as an empty one: a
// silent "nothing is tracked" would make every command refuse for a reason
// that has nothing to do with what the user asked.
func TestAnUnreadableStoreIsReportedNotTreatedAsEmpty(t *testing.T) {
	broken := graph.Service{
		Git:   g2gAncestry{current: "synthetic-b", local: []string{"synthetic-trunk", "synthetic-b"}},
		Store: &g2gStore{err: fmt.Errorf("synthetic store failure")},
	}

	if _, err := (G2GSelector{Service: broken}).Describes(context.Background(), "synthetic-b"); err == nil {
		t.Error("Describes() error = nil for an unreadable store")
	}
	if _, err := (G2GSelector{Service: broken}).Select(context.Background(), Selection{Branch: "synthetic-b"}, "g2g test"); err == nil {
		t.Error("Select() error = nil for an unreadable store")
	}
	if _, err := (G2GCandidates{Service: broken}).Branches(context.Background()); err == nil {
		t.Error("Branches() error = nil for an unreadable store")
	}
	if _, err := (G2GCandidates{Service: broken}).Trunks(context.Background(), "synthetic-b"); err == nil {
		t.Error("Trunks() error = nil for an unreadable store")
	}
}

// Ancestry is what revalidation compares to notice the structure above the
// base moving while the acted-on branches stay the same. A source that leaves
// it empty passes that comparison unconditionally, so a g2g-owned selection
// used to be revalidated more weakly than a Graphite or pull request one —
// invisibly, because the field was called GraphitePath.
func TestSelectorFillsTheAncestryRevalidationCompares(t *testing.T) {
	// The two scopes a projection command offers, because a GitHub native
	// stack is linear. Ancestry is the same either way: it describes the
	// structure, not the selection.
	for _, test := range []struct {
		scope Scope
		want  string
	}{
		{ScopePath, "synthetic-trunk,synthetic-a,synthetic-b"},
		{ScopeStack, "synthetic-trunk,synthetic-a,synthetic-b"},
	} {
		t.Run(string(test.scope), func(t *testing.T) {
			snapshot, err := selectorService(chain()).Select(context.Background(), Selection{Branch: "synthetic-b", Scope: test.scope}, "g2g test")
			if err != nil {
				t.Fatalf("Select() error = %v", err)
			}
			if got := strings.Join(snapshot.Ancestry, ","); got != test.want {
				t.Errorf("Ancestry = %q, want %q · the whole line of descent, not just the selection", got, test.want)
			}
		})
	}
}

// g2gAncestry and g2gStore are the graph service's two boundaries, reduced to
// what this selector actually asks them. They are copies of the graph
// package's own fakes rather than shared ones, because a fake shared across a
// package boundary is a second interface nobody declared.
type g2gAncestry struct {
	current   string
	local     []string
	ancestors map[string][]string
	err       error
}

func (f g2gAncestry) Resolve(_ context.Context, revision string) (string, error) {
	return revision, f.err
}

func (f g2gAncestry) CurrentBranch(context.Context) (string, error) { return f.current, f.err }

func (f g2gAncestry) LocalBranches(context.Context) ([]string, error) { return f.local, f.err }

func (f g2gAncestry) IsAncestor(_ context.Context, ancestor, descendant string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return slices.Contains(f.ancestors[descendant], ancestor), nil
}

func (f g2gAncestry) AncestorBranches(_ context.Context, branch string) ([]string, error) {
	return f.ancestors[branch], f.err
}

func (f g2gAncestry) Divergence(context.Context, string, string) (int, int, error) {
	return 0, 1, f.err
}

type g2gStore struct {
	graph graph.Graph
	err   error
}

func (s *g2gStore) Load(context.Context) (graph.Graph, error) {
	if s.err != nil {
		return graph.Graph{}, s.err
	}
	return s.graph.Clone(), nil
}

func (s *g2gStore) Save(_ context.Context, g graph.Graph) error {
	if s.err != nil {
		return s.err
	}
	s.graph = g.Clone()
	return nil
}

func (s *g2gStore) Path(context.Context) (string, error) {
	return "/synthetic/repo/.git/g2g/graph.json", nil
}

// Cherry reports every commit as absent from the trunk unless a case says
// otherwise, so a branch reads as landed only where that is the subject.
func (f g2gAncestry) Cherry(_ context.Context, _, head, _ string) (absent, present []string, err error) {
	return testutil.OwnCommits(head), nil, nil
}

// Absorbed answers of a whole branch what Cherry answers per commit, which is
// what a squash merge needs. Nothing here is absorbed unless a case says so.
func (f g2gAncestry) Absorbed(context.Context, string, string) (bool, error) { return false, nil }

// declaredChain declares synthetic-a a trunk that lands into synthetic-trunk,
// with synthetic-b stacked on it.
func declaredChain(t *testing.T, declaration graph.Declaration) graph.Graph {
	t.Helper()
	declared, err := chain().Untrack("synthetic-a").Declare("synthetic-a", declaration)
	if err != nil {
		t.Fatalf("Declare() error = %v", err)
	}
	return declared
}

// A declared trunk is claimed whether or not it lands anywhere: otherwise
// another source answers for it, and Graphite would place it back on the
// parent it was declared away from.
func TestSelectorDescribesEveryDeclaredTrunk(t *testing.T) {
	for name, declaration := range map[string]graph.Declaration{
		"landing nowhere":   {},
		"landing somewhere": {Into: "synthetic-trunk", By: "merge"},
	} {
		t.Run(name, func(t *testing.T) {
			describes, err := selectorService(declaredChain(t, declaration)).Describes(context.Background(), "synthetic-a")
			if err != nil || !describes {
				t.Errorf("Describes() = %t, %v", describes, err)
			}
		})
	}
}

// To a path it is a stack of one on where it lands; to every wider scope it is
// a trunk, which nothing describes from below. A trunk that lands nowhere is a
// trunk to every scope.
func TestSelectorAnswersADeclaredTrunkByScope(t *testing.T) {
	landing := declaredChain(t, graph.Declaration{Into: "synthetic-trunk", By: "merge"})
	for _, scope := range []Scope{ScopeBranch, ScopePath} {
		snapshot, err := selectorService(landing).Select(context.Background(), Selection{Branch: "synthetic-a", Scope: scope}, "g2g test")
		if err != nil {
			t.Fatalf("Select(%s) error = %v", scope, err)
		}
		if snapshot.Base != "synthetic-trunk" || !slices.Equal(snapshot.Branches, []string{"synthetic-a"}) || snapshot.Parents["synthetic-a"] != "synthetic-trunk" {
			t.Errorf("Select(%s) = base %q, branches %v, parents %v; want one branch on synthetic-trunk", scope, snapshot.Base, snapshot.Branches, snapshot.Parents)
		}
	}
	for _, scope := range []Scope{ScopeStack, ScopeSubtree, ScopeTrunk} {
		_, err := selectorService(landing).Select(context.Background(), Selection{Branch: "synthetic-a", Scope: scope}, "g2g test")
		var undescribed Undescribed
		if !errors.As(err, &undescribed) || !undescribed.Trunk || !strings.Contains(err.Error(), "g2g test --branch synthetic-a --scope path") {
			t.Errorf("Select(%s) error = %v, want a trunk that names the path", scope, err)
		}
	}
	_, err := selectorService(declaredChain(t, graph.Declaration{})).Select(context.Background(), Selection{Branch: "synthetic-a", Scope: ScopePath}, "g2g test")
	var undescribed Undescribed
	if !errors.As(err, &undescribed) || !undescribed.Trunk || !strings.Contains(err.Error(), "g2g create") {
		t.Errorf("Select() error = %v for a trunk landing nowhere, want a trunk to start a stack on", err)
	}
}

func TestSelectorRefusesTheWrongBaseForADeclaredTrunk(t *testing.T) {
	landing := declaredChain(t, graph.Declaration{Into: "synthetic-trunk", By: "merge"})
	if _, err := selectorService(landing).Select(context.Background(), Selection{Branch: "synthetic-a", Scope: ScopePath, Trunk: "synthetic-b"}, "g2g test"); err == nil {
		t.Error("Select() error = nil for a --trunk that is not where it lands")
	}

	gone, err := chain().Untrack("synthetic-a").Declare("synthetic-a", graph.Declaration{Into: "synthetic-gone", By: "merge"})
	if err != nil {
		t.Fatalf("Declare() error = %v", err)
	}
	if _, err := selectorService(gone).Select(context.Background(), Selection{Branch: "synthetic-a", Scope: ScopePath}, "g2g test"); err == nil {
		t.Error("Select() error = nil for somewhere to land that is not a local branch")
	}
}

// Completion offers a trunk that lands somewhere as a branch to act on, and
// where it lands as its trunk.
func TestCandidatesOfferADeclaredTrunkThatLandsSomewhere(t *testing.T) {
	candidates := G2GCandidates{Service: selectorService(declaredChain(t, graph.Declaration{Into: "synthetic-trunk", By: "merge"})).Service}
	branches, err := candidates.Branches(context.Background())
	if err != nil || !slices.Contains(branches, "synthetic-a") {
		t.Errorf("Branches() = %v, %v", branches, err)
	}
	if trunks, _ := candidates.Trunks(context.Background(), "synthetic-a"); !slices.Equal(trunks, []string{"synthetic-trunk"}) {
		t.Errorf("Trunks(synthetic-a) = %v", trunks)
	}
}

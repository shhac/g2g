package align

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/graphite"
)

// fakeGit answers the two questions an import asks about a branch: is it here,
// and does Git agree with the relationship Graphite declares.
type fakeGit struct {
	local       []string
	tips        map[string]string
	ancestors   map[string]string
	resolveErr  error
	ancestorErr error
}

func (g fakeGit) CurrentBranch(context.Context) (string, error) { return "synthetic-top", nil }
func (g fakeGit) LocalBranches(context.Context) ([]string, error) {
	return append([]string(nil), g.local...), nil
}
func (g fakeGit) AncestorBranches(context.Context, string) ([]string, error) {
	return nil, nil
}
func (g fakeGit) Divergence(context.Context, string, string) (int, int, error) { return 0, 0, nil }
func (g fakeGit) IsAncestor(_ context.Context, ancestor, descendant string) (bool, error) {
	if g.ancestorErr != nil {
		return false, g.ancestorErr
	}
	return g.ancestors[descendant] == ancestor, nil
}
func (g fakeGit) Resolve(_ context.Context, revision string) (string, error) {
	if g.resolveErr != nil {
		return "", g.resolveErr
	}
	if tip, ok := g.tips[revision]; ok {
		return tip, nil
	}
	return "0000000000000000000000000000000000000000", nil
}

func importService(adopted graph.Graph, forest graphite.Forest, git fakeGit) (Service, *memoryStore) {
	store := &memoryStore{graph: adopted}
	return Service{
		Git: git, Store: store,
		Graphite: &fakeGraphite{forest: forest},
	}, store
}

func declaredChain() graphite.Forest {
	return forestOf(map[string]string{
		"synthetic-trunk": "",
		"synthetic-lower": "synthetic-trunk",
		"synthetic-top":   "synthetic-lower",
	}, "synthetic-trunk")
}

func everyBranchLocal() fakeGit {
	return fakeGit{
		local: []string{"synthetic-trunk", "synthetic-lower", "synthetic-top"},
		tips:  map[string]string{"synthetic-trunk": "aaaa", "synthetic-lower": "bbbb"},
		ancestors: map[string]string{
			"synthetic-lower": "synthetic-trunk",
			"synthetic-top":   "synthetic-lower",
		},
	}
}

// Graphite declares each parent, so this is not the guess track refuses to
// make. Parents are recorded before the branches that name them.
func TestImportAdoptsWhatGraphiteDeclares(t *testing.T) {
	svc, store := importService(graph.New(), declaredChain(), everyBranchLocal())

	plan, err := svc.PlanImport(context.Background())
	if err != nil {
		t.Fatalf("PlanImport() error = %v", err)
	}
	if got, want := strings.Join(plan.Claims(), ","), "synthetic-lower,synthetic-top"; got != want {
		t.Fatalf("Claims() = %s, want %s", got, want)
	}
	if err := svc.ApplyImport(context.Background(), plan); err != nil {
		t.Fatalf("ApplyImport() error = %v", err)
	}

	if parent, _ := store.graph.Parent("synthetic-top"); parent != "synthetic-lower" {
		t.Errorf("parent of synthetic-top = %q", parent)
	}
	if !store.graph.IsTrunk("synthetic-trunk") {
		t.Errorf("Trunks = %v, want the Graphite root recorded", store.graph.Trunks)
	}
}

// The fork point cannot be derived later, so it is manufactured now, from the
// parent's tip. Graphite has no field to copy it from.
func TestImportRecordsAForkPointGraphiteCannotSupply(t *testing.T) {
	svc, store := importService(graph.New(), declaredChain(), everyBranchLocal())

	plan, err := svc.PlanImport(context.Background())
	if err != nil {
		t.Fatalf("PlanImport() error = %v", err)
	}
	if err := svc.ApplyImport(context.Background(), plan); err != nil {
		t.Fatalf("ApplyImport() error = %v", err)
	}

	if got := store.graph.Edges["synthetic-top"].ForkPoint; got != "bbbb" {
		t.Errorf("fork point of synthetic-top = %q, want the parent's tip", got)
	}
	for _, adoption := range plan.Adopt {
		if adoption.ForkPoint == "" {
			t.Errorf("plan did not carry the fork point it wrote for %s", adoption.Branch)
		}
	}
}

// Origin says how far Git agrees with an edge, not which tool supplied it, so
// a declared relationship the commits do not show is still recorded as an
// assertion.
func TestImportAssessesEachEdgeAgainstGit(t *testing.T) {
	git := everyBranchLocal()
	git.ancestors = map[string]string{"synthetic-lower": "synthetic-trunk"}
	svc, store := importService(graph.New(), declaredChain(), git)

	plan, err := svc.PlanImport(context.Background())
	if err != nil {
		t.Fatalf("PlanImport() error = %v", err)
	}
	if err := svc.ApplyImport(context.Background(), plan); err != nil {
		t.Fatalf("ApplyImport() error = %v", err)
	}

	if got := store.graph.Edges["synthetic-lower"].Origin; got != graph.OriginAncestry {
		t.Errorf("origin of the confirmed edge = %q, want %q", got, graph.OriginAncestry)
	}
	if got := store.graph.Edges["synthetic-top"].Origin; got != graph.OriginUser {
		t.Errorf("origin of the unconfirmed edge = %q, want %q", got, graph.OriginUser)
	}
}

// The one thing an additive command must not do is silently undo a deliberate
// g2g change, so a disagreement blocks and names both answers.
func TestImportBlocksOnADisagreement(t *testing.T) {
	ours := graph.Graph{
		Edges:  map[string]graph.Edge{"synthetic-top": {Parent: "synthetic-trunk"}},
		Trunks: []string{"synthetic-trunk"},
	}
	svc, store := importService(ours, declaredChain(), everyBranchLocal())

	plan, err := svc.PlanImport(context.Background())
	if err != nil {
		t.Fatalf("PlanImport() error = %v", err)
	}
	if len(plan.Conflicts) != 1 {
		t.Fatalf("Conflicts = %v, want the disagreement reported", plan.Conflicts)
	}
	conflict := plan.Conflicts[0]
	if conflict.Ours != "synthetic-trunk" || conflict.Theirs != "synthetic-lower" {
		t.Errorf("conflict = %+v, want both answers named", conflict)
	}
	if err := svc.ApplyImport(context.Background(), plan); err == nil {
		t.Error("ApplyImport() error = nil for a blocked plan")
	}
	if store.graph.Edges["synthetic-top"].Parent != "synthetic-trunk" {
		t.Error("a blocked import changed the g2g graph")
	}
}

// Re-running over branches both records already agree about does nothing, which
// is what makes import safe to repeat when someone tracks a new branch in gt.
func TestImportIsRepeatable(t *testing.T) {
	svc, store := importService(graph.New(), declaredChain(), everyBranchLocal())

	first, err := svc.PlanImport(context.Background())
	if err != nil {
		t.Fatalf("PlanImport() error = %v", err)
	}
	if err := svc.ApplyImport(context.Background(), first); err != nil {
		t.Fatalf("ApplyImport() error = %v", err)
	}

	second, err := svc.PlanImport(context.Background())
	if err != nil {
		t.Fatalf("second PlanImport() error = %v", err)
	}
	if len(second.Adopt) != 0 {
		t.Errorf("Adopt = %v on a second run, want nothing", second.Claims())
	}
	if got, want := strings.Join(second.Agreed, ","), "synthetic-lower,synthetic-top"; got != want {
		t.Errorf("Agreed = %s, want %s", got, want)
	}
	before := store.graph
	if err := svc.ApplyImport(context.Background(), second); err != nil {
		t.Fatalf("second ApplyImport() error = %v", err)
	}
	if !store.graph.Equal(before) {
		t.Error("a second import changed the graph")
	}
}

// Graphite can name a branch this checkout does not have. Recording an edge for
// one would put a branch in the graph that no command could act on.
func TestImportSkipsBranchesThatAreNotLocal(t *testing.T) {
	git := fakeGit{local: []string{"synthetic-trunk", "synthetic-lower"}, tips: map[string]string{"synthetic-trunk": "aaaa"}}
	svc, _ := importService(graph.New(), declaredChain(), git)

	plan, err := svc.PlanImport(context.Background())
	if err != nil {
		t.Fatalf("PlanImport() error = %v", err)
	}
	if got, want := strings.Join(plan.Claims(), ","), "synthetic-lower"; got != want {
		t.Errorf("Claims() = %s, want %s", got, want)
	}
}

// Import writes the g2g graph and nothing else. Graphite keeps every branch
// it had; the only change is which record answers.
func TestImportWritesNothingToGraphite(t *testing.T) {
	client := &fakeGraphite{forest: declaredChain()}
	svc := Service{
		Git: everyBranchLocal(), Store: &memoryStore{graph: graph.New()},
		Graphite: client,
	}

	plan, err := svc.PlanImport(context.Background())
	if err != nil {
		t.Fatalf("PlanImport() error = %v", err)
	}
	if err := svc.ApplyImport(context.Background(), plan); err != nil {
		t.Fatalf("ApplyImport() error = %v", err)
	}
	if got := client.recorded(); got != "" {
		t.Errorf("recorded %q, want import to write nothing to Graphite", got)
	}
}

func TestRevalidateImportRefusesAChangedGraph(t *testing.T) {
	svc, store := importService(graph.New(), declaredChain(), everyBranchLocal())

	preview, err := svc.PlanImport(context.Background())
	if err != nil {
		t.Fatalf("PlanImport() error = %v", err)
	}
	store.graph = graph.Graph{
		Edges:  map[string]graph.Edge{"synthetic-top": {Parent: "synthetic-trunk"}},
		Trunks: []string{"synthetic-trunk"},
	}

	if _, err := svc.RevalidateImport(context.Background(), preview); err == nil {
		t.Error("RevalidateImport() error = nil after the graph moved")
	}
}

// fakeRefs records what was pinned. The fork point is the one thing import
// manufactures that Graphite cannot supply, so "a ref was written" is not the
// assertion that matters — "the right branch, at the right object" is.
type fakeRefs struct {
	pinned map[string]string
	err    error
}

func (r *fakeRefs) PinForkPoint(_ context.Context, branch, object string) error {
	if r.err != nil {
		return r.err
	}
	if r.pinned == nil {
		r.pinned = map[string]string{}
	}
	r.pinned[branch] = object
	return nil
}

func (r *fakeRefs) UnpinForkPoint(context.Context, string) error { return nil }

func TestImportPinsEachForkPointItRecords(t *testing.T) {
	refs := &fakeRefs{}
	store := &memoryStore{graph: graph.New()}
	svc := Service{
		Git: everyBranchLocal(), Store: store, Refs: refs,
		Graphite: &fakeGraphite{forest: declaredChain()},
	}

	plan, err := svc.PlanImport(context.Background())
	if err != nil {
		t.Fatalf("PlanImport() error = %v", err)
	}
	if err := svc.ApplyImport(context.Background(), plan); err != nil {
		t.Fatalf("ApplyImport() error = %v", err)
	}

	want := map[string]string{"synthetic-lower": "aaaa", "synthetic-top": "bbbb"}
	for branch, object := range want {
		if refs.pinned[branch] != object {
			t.Errorf("pinned %s at %q, want %q", branch, refs.pinned[branch], object)
		}
	}
	if len(refs.pinned) != len(want) {
		t.Errorf("pinned %v, want exactly %v", refs.pinned, want)
	}
	// What was pinned must be what the plan promised, or the preview lied.
	for _, adoption := range plan.Adopt {
		if refs.pinned[adoption.Branch] != adoption.ForkPoint {
			t.Errorf("%s pinned at %q but the plan said %q", adoption.Branch, refs.pinned[adoption.Branch], adoption.ForkPoint)
		}
	}
}

// A pin that fails is reported rather than swallowed: the graph is written but
// the fork point is not protected, and the user needs to know.
func TestImportReportsAFailedPin(t *testing.T) {
	svc := Service{
		Git:      everyBranchLocal(),
		Store:    &memoryStore{graph: graph.New()},
		Refs:     &fakeRefs{err: fmt.Errorf("synthetic ref failure")},
		Graphite: &fakeGraphite{forest: declaredChain()},
	}

	plan, err := svc.PlanImport(context.Background())
	if err != nil {
		t.Fatalf("PlanImport() error = %v", err)
	}
	if err := svc.ApplyImport(context.Background(), plan); err == nil {
		t.Error("ApplyImport() error = nil when the fork point could not be pinned")
	}
}

// Git failing mid-plan must fail the whole plan, not return a half-built one a
// caller might act on.
func TestImportFailsClosedWhenGitCannotAnswer(t *testing.T) {
	for name, git := range map[string]fakeGit{
		"resolve":    {local: []string{"synthetic-trunk", "synthetic-lower", "synthetic-top"}, resolveErr: fmt.Errorf("synthetic resolve failure")},
		"isAncestor": {local: []string{"synthetic-trunk", "synthetic-lower", "synthetic-top"}, ancestorErr: fmt.Errorf("synthetic ancestry failure")},
	} {
		t.Run(name, func(t *testing.T) {
			store := &memoryStore{graph: graph.New()}
			svc := Service{
				Git: git, Store: store,
				Graphite: &fakeGraphite{forest: declaredChain()},
			}

			if _, err := svc.PlanImport(context.Background()); err == nil {
				t.Fatalf("PlanImport() error = nil when Git could not answer")
			}
			if len(store.writes) != 0 {
				t.Error("a failed plan wrote to the graph")
			}
		})
	}
}

// Revalidation must catch a plan whose shape is unchanged but whose content
// moved. Comparing lengths alone would let a stale plan through.
func TestRevalidateImportCatchesAChangedParentAtTheSameCount(t *testing.T) {
	store := &memoryStore{graph: graph.New()}
	client := &fakeGraphite{forest: forestOf(map[string]string{
		"synthetic-trunk": "",
		"synthetic-lower": "synthetic-trunk",
	}, "synthetic-trunk")}
	svc := Service{Git: everyBranchLocal(), Store: store, Graphite: client}

	preview, err := svc.PlanImport(context.Background())
	if err != nil {
		t.Fatalf("PlanImport() error = %v", err)
	}
	if len(preview.Adopt) != 1 {
		t.Fatalf("Adopt = %v, want exactly one so the count cannot change", preview.Claims())
	}

	// Same number of adoptions, different parent.
	client.forest = forestOf(map[string]string{
		"synthetic-top":   "",
		"synthetic-lower": "synthetic-top",
	}, "synthetic-top")

	if _, err := svc.RevalidateImport(context.Background(), preview); err == nil {
		t.Error("RevalidateImport() error = nil when the parent moved at an unchanged adoption count")
	}
}

// The enrolment gate is shared with mirror, but import must be proven to keep
// it: a future refactor giving import its own read path would otherwise break
// the invariant with nothing catching it.
func TestImportRefusesToAskAGraphiteFreeRepository(t *testing.T) {
	asked := false
	svc := Service{
		Git: everyBranchLocal(), Store: &memoryStore{graph: graph.New()},
		Graphite:   &fakeGraphite{forest: declaredChain(), asked: &asked},
		Configured: func(context.Context) (bool, error) { return false, nil },
	}

	if _, err := svc.PlanImport(context.Background()); err == nil {
		t.Error("PlanImport() error = nil in a repository that does not use Graphite")
	}
	if asked {
		t.Error("import read Graphite in a repository that does not use it, which is what enrols it")
	}
}

// mirror has an end-to-end test for a failing Graphite write; import's
// equivalent risky write is the graph store itself, and a failure there must
// be reported rather than followed by a false "adopted".
func TestImportReportsAFailedGraphWrite(t *testing.T) {
	store := &memoryStore{graph: graph.New()}
	refs := &fakeRefs{}
	svc := Service{
		Git:      everyBranchLocal(),
		Store:    store,
		Refs:     refs,
		Graphite: &fakeGraphite{forest: declaredChain()},
	}

	plan, err := svc.PlanImport(context.Background())
	if err != nil {
		t.Fatalf("PlanImport() error = %v", err)
	}
	if len(plan.Adopt) == 0 {
		t.Fatal("nothing to adopt, so the write is never reached")
	}

	store.err = fmt.Errorf("synthetic store failure")
	if err := svc.ApplyImport(context.Background(), plan); err == nil {
		t.Error("ApplyImport() error = nil when the graph could not be written")
	}
	if len(refs.pinned) != 0 {
		t.Errorf("pinned %v for a graph that was never saved", refs.pinned)
	}
}

// Cherry reports every commit as absent from the trunk, so nothing here reads
// as landed by content unless a case is about that.
func (f fakeGit) Cherry(_ context.Context, _, head, _ string) (absent, present []string, err error) {
	return []string{head + "-own-commit"}, nil, nil
}

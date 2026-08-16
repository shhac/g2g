package align

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/graph"
	"github.com/shhac/gt2gh/internal/graphite"
)

// fakeGraphite records what alignment asked Graphite to do, in order. Ordering
// is a correctness property here, not a detail: Graphite refuses a parent it
// does not track, and untracking cascades.
type fakeGraphite struct {
	forest graphite.Forest
	calls  []string
	err    error
}

func (f *fakeGraphite) ReadForest(context.Context) (graphite.Forest, error) {
	if f.err != nil {
		return graphite.Forest{}, f.err
	}
	return f.forest, nil
}

func (f *fakeGraphite) Track(_ context.Context, branch, parent string) error {
	f.calls = append(f.calls, "track "+branch+" onto "+parent)
	return f.err
}

func (f *fakeGraphite) Untrack(_ context.Context, branch string) error {
	f.calls = append(f.calls, "untrack "+branch)
	return f.err
}

func (f *fakeGraphite) recorded() string { return strings.Join(f.calls, "; ") }

type memoryStore struct{ graph graph.Graph }

func (m *memoryStore) Load(context.Context) (graph.Graph, error) { return m.graph, nil }
func (m *memoryStore) Save(_ context.Context, g graph.Graph) error {
	m.graph = g
	return nil
}
func (m *memoryStore) Path(context.Context) (string, error) { return "/synthetic/graph.json", nil }

func forestOf(parents map[string]string, roots ...string) graphite.Forest {
	return graphite.Forest{Parents: parents, Roots: roots}
}

// chain is the gt2gh graph under test: trunk <- lower <- top.
func chain() graph.Graph {
	return graph.Graph{
		Edges: map[string]graph.Edge{
			"synthetic-lower": {Parent: "synthetic-trunk"},
			"synthetic-top":   {Parent: "synthetic-lower"},
		},
		Trunks: []string{"synthetic-trunk"},
	}
}

func service(adopted graph.Graph, forest graphite.Forest) (Service, *fakeGraphite) {
	client := &fakeGraphite{forest: forest}
	return Service{Graph: graph.Service{Store: &memoryStore{graph: adopted}}, Graphite: client}, client
}

// Graphite refuses a parent it does not already track, so a chain has to be
// written from the root down.
func TestMirrorWritesParentsBeforeChildren(t *testing.T) {
	svc, client := service(chain(), forestOf(map[string]string{"synthetic-trunk": ""}, "synthetic-trunk"))

	plan, err := svc.PlanMirror(context.Background(), false)
	if err != nil {
		t.Fatalf("PlanMirror() error = %v", err)
	}
	if err := svc.ApplyMirror(context.Background(), plan); err != nil {
		t.Fatalf("ApplyMirror() error = %v", err)
	}

	want := "track synthetic-lower onto synthetic-trunk; track synthetic-top onto synthetic-lower"
	if got := client.recorded(); got != want {
		t.Errorf("recorded %q, want %q", got, want)
	}
}

// An edge Graphite already has right is left alone. Alignment is a diff, not a
// rewrite.
func TestMirrorWritesNothingWhenAlreadyAligned(t *testing.T) {
	aligned := forestOf(map[string]string{
		"synthetic-trunk": "",
		"synthetic-lower": "synthetic-trunk",
		"synthetic-top":   "synthetic-lower",
	}, "synthetic-trunk")
	svc, client := service(chain(), aligned)

	plan, err := svc.PlanMirror(context.Background(), true)
	if err != nil {
		t.Fatalf("PlanMirror() error = %v", err)
	}
	if !plan.Aligned() {
		t.Errorf("plan is not aligned: writes=%v prunes=%v", plan.Writes, plan.Prunes)
	}
	if err := svc.ApplyMirror(context.Background(), plan); err != nil {
		t.Fatalf("ApplyMirror() error = %v", err)
	}
	if got := client.recorded(); got != "" {
		t.Errorf("recorded %q, want nothing", got)
	}
}

// A parent that disagrees is moved, and reported as a move rather than an add.
func TestMirrorMovesADisagreeingParent(t *testing.T) {
	stale := forestOf(map[string]string{
		"synthetic-trunk": "",
		"synthetic-lower": "synthetic-trunk",
		"synthetic-top":   "synthetic-trunk",
	}, "synthetic-trunk")
	svc, client := service(chain(), stale)

	plan, err := svc.PlanMirror(context.Background(), false)
	if err != nil {
		t.Fatalf("PlanMirror() error = %v", err)
	}
	if got := strings.Join(plan.Moved(), ","); got != "synthetic-top" {
		t.Errorf("Moved() = %s, want synthetic-top", got)
	}
	if got := strings.Join(plan.Added(), ","); got != "" {
		t.Errorf("Added() = %s, want nothing", got)
	}
	if err := svc.ApplyMirror(context.Background(), plan); err != nil {
		t.Fatalf("ApplyMirror() error = %v", err)
	}
	if got, want := client.recorded(), "track synthetic-top onto synthetic-lower"; got != want {
		t.Errorf("recorded %q, want %q", got, want)
	}
}

// Graphite cannot be told about a branch without naming a parent it already
// tracks, so a root it has never heard of blocks rather than guessing.
func TestMirrorBlocksOnARootGraphiteDoesNotKnow(t *testing.T) {
	svc, client := service(chain(), forestOf(map[string]string{"synthetic-elsewhere": ""}, "synthetic-elsewhere"))

	plan, err := svc.PlanMirror(context.Background(), false)
	if err != nil {
		t.Fatalf("PlanMirror() error = %v", err)
	}
	if !strings.Contains(plan.Blocked, "synthetic-trunk") {
		t.Errorf("Blocked = %q, want it to name the root Graphite lacks", plan.Blocked)
	}
	if err := svc.ApplyMirror(context.Background(), plan); err == nil {
		t.Error("ApplyMirror() error = nil for a blocked plan")
	}
	if got := client.recorded(); got != "" {
		t.Errorf("recorded %q, want a blocked plan to write nothing", got)
	}
}

// Branches gt2gh says nothing about are reported but untouched without --prune.
func TestMirrorLeavesStrangersAloneWithoutPrune(t *testing.T) {
	withStranger := forestOf(map[string]string{
		"synthetic-trunk":   "",
		"synthetic-lower":   "synthetic-trunk",
		"synthetic-top":     "synthetic-lower",
		"synthetic-someone": "synthetic-trunk",
	}, "synthetic-trunk")
	svc, client := service(chain(), withStranger)

	plan, err := svc.PlanMirror(context.Background(), false)
	if err != nil {
		t.Fatalf("PlanMirror() error = %v", err)
	}
	if got := strings.Join(plan.Strangers, ","); got != "synthetic-someone" {
		t.Errorf("Strangers = %s", got)
	}
	if len(plan.Prunes) != 0 {
		t.Errorf("Prunes = %v without --prune", plan.Prunes)
	}
	if err := svc.ApplyMirror(context.Background(), plan); err != nil {
		t.Fatalf("ApplyMirror() error = %v", err)
	}
	if got := client.recorded(); got != "" {
		t.Errorf("recorded %q, want nothing written", got)
	}
}

// Untracking cascades to the subtree, so prunes run deepest first. Removing a
// parent first would take its child with it and leave the plan lying about
// what it did.
func TestPruneRemovesDeepestFirst(t *testing.T) {
	nested := forestOf(map[string]string{
		"synthetic-trunk":  "",
		"synthetic-lower":  "synthetic-trunk",
		"synthetic-top":    "synthetic-lower",
		"synthetic-stale":  "synthetic-trunk",
		"synthetic-staler": "synthetic-stale",
	}, "synthetic-trunk")
	svc, client := service(chain(), nested)

	plan, err := svc.PlanMirror(context.Background(), true)
	if err != nil {
		t.Fatalf("PlanMirror() error = %v", err)
	}
	if got, want := strings.Join(plan.Prunes, ","), "synthetic-staler,synthetic-stale"; got != want {
		t.Errorf("Prunes = %s, want %s", got, want)
	}
	if err := svc.ApplyMirror(context.Background(), plan); err != nil {
		t.Fatalf("ApplyMirror() error = %v", err)
	}
	if got, want := client.recorded(), "untrack synthetic-staler; untrack synthetic-stale"; got != want {
		t.Errorf("recorded %q, want %q", got, want)
	}
}

// The hazard the whole prune design exists for: untracking a stranger whose
// child gt2gh does know would silently untrack the branch the mirror just
// aligned. It is shielded instead.
func TestPruneRefusesToTakeAKnownBranchWithIt(t *testing.T) {
	adopted := graph.Graph{
		Edges: map[string]graph.Edge{
			"synthetic-lower": {Parent: "synthetic-trunk"},
			"synthetic-top":   {Parent: "synthetic-lower"},
		},
		Trunks: []string{"synthetic-trunk"},
	}
	// Graphite has synthetic-bridge, which gt2gh does not know, and it is the
	// declared parent of synthetic-top, which gt2gh does.
	bridged := forestOf(map[string]string{
		"synthetic-trunk":  "",
		"synthetic-lower":  "synthetic-trunk",
		"synthetic-bridge": "synthetic-trunk",
		"synthetic-top":    "synthetic-bridge",
	}, "synthetic-trunk")
	svc, client := service(adopted, bridged)

	plan, err := svc.PlanMirror(context.Background(), true)
	if err != nil {
		t.Fatalf("PlanMirror() error = %v", err)
	}
	if len(plan.Prunes) != 0 {
		t.Errorf("Prunes = %v, want the bridge shielded by its known child", plan.Prunes)
	}
	if got := strings.Join(plan.Shielded(), ","); got != "synthetic-bridge" {
		t.Errorf("Shielded() = %s, want synthetic-bridge", got)
	}
	if err := svc.ApplyMirror(context.Background(), plan); err != nil {
		t.Fatalf("ApplyMirror() error = %v", err)
	}
	// The move is still made; only the removal is withheld.
	if got, want := client.recorded(), "track synthetic-top onto synthetic-lower"; got != want {
		t.Errorf("recorded %q, want %q", got, want)
	}
}

// A stranger whose only children are also being pruned is removed, deepest
// first, because nothing gt2gh knows goes with it.
func TestPruneRemovesAWholeStrangerSubtree(t *testing.T) {
	svc, _ := service(chain(), forestOf(map[string]string{
		"synthetic-trunk": "",
		"synthetic-lower": "synthetic-trunk",
		"synthetic-top":   "synthetic-lower",
		"synthetic-a":     "synthetic-trunk",
		"synthetic-b":     "synthetic-a",
	}, "synthetic-trunk"))

	plan, err := svc.PlanMirror(context.Background(), true)
	if err != nil {
		t.Fatalf("PlanMirror() error = %v", err)
	}
	if got, want := strings.Join(plan.Prunes, ","), "synthetic-b,synthetic-a"; got != want {
		t.Errorf("Prunes = %s, want %s", got, want)
	}
	if len(plan.Shielded()) != 0 {
		t.Errorf("Shielded() = %v, want none", plan.Shielded())
	}
}

// A graph that moved between preview and apply is caught rather than acted on.
func TestRevalidateRefusesAChangedGraph(t *testing.T) {
	store := &memoryStore{graph: chain()}
	client := &fakeGraphite{forest: forestOf(map[string]string{"synthetic-trunk": ""}, "synthetic-trunk")}
	svc := Service{Graph: graph.Service{Store: store}, Graphite: client}

	preview, err := svc.PlanMirror(context.Background(), false)
	if err != nil {
		t.Fatalf("PlanMirror() error = %v", err)
	}
	store.graph = graph.Graph{Edges: map[string]graph.Edge{"synthetic-lower": {Parent: "synthetic-trunk"}}, Trunks: []string{"synthetic-trunk"}}

	if _, err := svc.RevalidateMirror(context.Background(), false, preview); err == nil {
		t.Error("RevalidateMirror() error = nil after the graph moved")
	}
}

func TestRevalidateAcceptsAnUnchangedGraph(t *testing.T) {
	svc, _ := service(chain(), forestOf(map[string]string{"synthetic-trunk": ""}, "synthetic-trunk"))

	preview, err := svc.PlanMirror(context.Background(), false)
	if err != nil {
		t.Fatalf("PlanMirror() error = %v", err)
	}
	if _, err := svc.RevalidateMirror(context.Background(), false, preview); err != nil {
		t.Errorf("RevalidateMirror() error = %v for an unchanged graph", err)
	}
}

func TestAnUnconfiguredServiceRefuses(t *testing.T) {
	if _, err := (Service{}).PlanMirror(context.Background(), false); err == nil {
		t.Error("PlanMirror() error = nil on an unconfigured service")
	}
}

func TestAFailingGraphiteIsReported(t *testing.T) {
	svc := Service{
		Graph:    graph.Service{Store: &memoryStore{graph: chain()}},
		Graphite: &fakeGraphite{err: fmt.Errorf("synthetic Graphite failure")},
	}

	if _, err := svc.PlanMirror(context.Background(), false); err == nil {
		t.Error("PlanMirror() error = nil when Graphite could not be read")
	}
}

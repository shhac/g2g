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
	asked  *bool
	// failAfter makes the (failAfter+1)th write fail, so a test can assert what
	// a mirror does when Graphite refuses part-way through a sequence.
	failAfter int
	writeErr  error
}

func (f *fakeGraphite) ReadForest(context.Context) (graphite.Forest, error) {
	if f.asked != nil {
		*f.asked = true
	}
	if f.err != nil {
		return graphite.Forest{}, f.err
	}
	return f.forest, nil
}

func (f *fakeGraphite) Track(_ context.Context, branch, parent string) error {
	f.calls = append(f.calls, "track "+branch+" onto "+parent)
	return f.writeFailure()
}

func (f *fakeGraphite) Untrack(_ context.Context, branch string) error {
	f.calls = append(f.calls, "untrack "+branch)
	return f.writeFailure()
}

func (f *fakeGraphite) writeFailure() error {
	if f.writeErr != nil && len(f.calls) > f.failAfter {
		return f.writeErr
	}
	return f.err
}

func (f *fakeGraphite) recorded() string { return strings.Join(f.calls, "; ") }

type memoryStore struct {
	graph  graph.Graph
	writes []graph.Graph
	// err makes the store unusable, so a test can prove a failed write is
	// reported rather than followed by a success message.
	err error
}

func (m *memoryStore) Load(context.Context) (graph.Graph, error) { return m.graph, m.err }
func (m *memoryStore) Save(_ context.Context, g graph.Graph) error {
	if m.err != nil {
		return m.err
	}
	m.writes = append(m.writes, g.Clone())
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
	return Service{Store: &memoryStore{graph: adopted}, Graphite: client}, client
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
	if plan.Blocked == "" {
		t.Error("Blocked = empty for a root Graphite does not track")
	}
	// The name is carried, not rendered: the preview composes the sentence with
	// the same helper every other branch list in the output uses.
	if got := strings.Join(plan.UnknownRoots, ","); got != "synthetic-trunk" {
		t.Errorf("UnknownRoots = %s, want the root Graphite lacks", got)
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
	svc := Service{Store: store, Graphite: client}

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
		Store:    &memoryStore{graph: chain()},
		Graphite: &fakeGraphite{err: fmt.Errorf("synthetic Graphite failure")},
	}

	if _, err := svc.PlanMirror(context.Background(), false); err == nil {
		t.Error("PlanMirror() error = nil when Graphite could not be read")
	}
}

// The invariant the whole project rests on: alignment is not ownership
// transfer. A mirror — prune and all — must leave the gt2gh graph byte for byte
// as it found it.
func TestMirrorNeverChangesTheG2GGraph(t *testing.T) {
	store := &memoryStore{graph: chain()}
	before := store.graph.Clone()
	client := &fakeGraphite{forest: forestOf(map[string]string{
		"synthetic-trunk": "",
		"synthetic-top":   "synthetic-trunk",
		"synthetic-stale": "synthetic-trunk",
	}, "synthetic-trunk")}
	svc := Service{Store: store, Graphite: client}

	plan, err := svc.PlanMirror(context.Background(), true)
	if err != nil {
		t.Fatalf("PlanMirror() error = %v", err)
	}
	if len(plan.Writes) == 0 || len(plan.Prunes) == 0 {
		t.Fatalf("plan does nothing, so the test proves nothing: %+v", plan)
	}
	if err := svc.ApplyMirror(context.Background(), plan); err != nil {
		t.Fatalf("ApplyMirror() error = %v", err)
	}

	if !store.graph.Equal(before) {
		t.Errorf("mirror changed the gt2gh graph: %+v, want %+v", store.graph, before)
	}
	if len(store.writes) != 0 {
		t.Errorf("mirror wrote to the gt2gh store %d times, want none", len(store.writes))
	}
}

// The design doc claims a mirror "does not unwind... re-running is how the rest
// gets done". Nothing exercised that claim: no test made Graphite refuse a
// write. This does, and pins both halves — it stops, and it stops where it said.
func TestMirrorStopsAtTheFirstWriteGraphiteRefuses(t *testing.T) {
	client := &fakeGraphite{
		forest:    forestOf(map[string]string{"synthetic-trunk": ""}, "synthetic-trunk"),
		failAfter: 1,
		writeErr:  fmt.Errorf("synthetic Graphite refusal"),
	}
	svc := Service{Store: &memoryStore{graph: chain()}, Graphite: client}

	plan, err := svc.PlanMirror(context.Background(), false)
	if err != nil {
		t.Fatalf("PlanMirror() error = %v", err)
	}
	if len(plan.Writes) != 2 {
		t.Fatalf("Writes = %v, want two so a mid-sequence failure is observable", plan.Writes)
	}

	if err := svc.ApplyMirror(context.Background(), plan); err == nil {
		t.Fatal("ApplyMirror() error = nil when Graphite refused a write")
	}
	// The first write happened, the second was attempted and failed, and nothing
	// after it ran.
	if got, want := client.recorded(), "track synthetic-lower onto synthetic-trunk; track synthetic-top onto synthetic-lower"; got != want {
		t.Errorf("recorded %q, want %q", got, want)
	}
}

// A prune that fails must not carry on removing. Untracking cascades, so
// continuing past a failure is how a mirror would take branches nobody asked it
// to.
func TestMirrorStopsWhenAPruneFails(t *testing.T) {
	client := &fakeGraphite{
		forest: forestOf(map[string]string{
			"synthetic-trunk": "",
			"synthetic-lower": "synthetic-trunk",
			"synthetic-top":   "synthetic-lower",
			"synthetic-a":     "synthetic-trunk",
			"synthetic-b":     "synthetic-trunk",
		}, "synthetic-trunk"),
		writeErr: fmt.Errorf("synthetic Graphite refusal"),
	}
	svc := Service{Store: &memoryStore{graph: chain()}, Graphite: client}

	plan, err := svc.PlanMirror(context.Background(), true)
	if err != nil {
		t.Fatalf("PlanMirror() error = %v", err)
	}
	if len(plan.Prunes) != 2 {
		t.Fatalf("Prunes = %v, want two so a mid-sequence failure is observable", plan.Prunes)
	}

	if err := svc.ApplyMirror(context.Background(), plan); err == nil {
		t.Fatal("ApplyMirror() error = nil when a prune failed")
	}
	if got := len(client.calls); got != 1 {
		t.Errorf("made %d calls (%s), want to stop after the first failure", got, client.recorded())
	}
}

// Revalidation must catch a plan whose shape is unchanged but whose content
// moved. Comparing lengths alone would let a stale plan through and mirror the
// wrong parent.
func TestRevalidateMirrorCatchesAChangedParentAtTheSameCount(t *testing.T) {
	client := &fakeGraphite{forest: forestOf(map[string]string{
		"synthetic-trunk": "",
		"synthetic-lower": "synthetic-trunk",
		"synthetic-top":   "synthetic-trunk",
	}, "synthetic-trunk")}
	svc := Service{Store: &memoryStore{graph: chain()}, Graphite: client}

	preview, err := svc.PlanMirror(context.Background(), false)
	if err != nil {
		t.Fatalf("PlanMirror() error = %v", err)
	}
	if len(preview.Writes) != 1 {
		t.Fatalf("Writes = %v, want exactly one so the count cannot change", preview.Writes)
	}

	// Still exactly one write to make, but for a different branch.
	client.forest = forestOf(map[string]string{
		"synthetic-trunk": "",
		"synthetic-lower": "synthetic-top",
		"synthetic-top":   "synthetic-lower",
	}, "synthetic-trunk")

	if _, err := svc.RevalidateMirror(context.Background(), false, preview); err == nil {
		t.Error("RevalidateMirror() error = nil when a write changed at an unchanged count")
	}
}

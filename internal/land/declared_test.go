package land

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
)

func declaredGraph(t *testing.T) graph.Graph {
	t.Helper()
	recorded := graph.Graph{
		Edges:  map[string]graph.Edge{"synthetic-a1": {Parent: "synthetic-feature"}},
		Trunks: []string{"main"},
	}
	recorded, err := recorded.Declare("synthetic-feature", graph.Declaration{Into: "main", By: "rebase"})
	if err != nil {
		t.Fatalf("Declare() error = %v", err)
	}
	return recorded
}

func TestDeclaredOnlyWhenLandingIntoWhereItIsDeclaredToGo(t *testing.T) {
	recorded := declaredGraph(t)
	onto := func(base string) stack.Discovery {
		return stack.Discovery{Snapshot: stack.Snapshot{Target: "synthetic-feature", Base: base, Source: stack.SourceG2G}}
	}
	if got := declared(recorded, onto("main")); got.Into != "main" {
		t.Errorf("declared() = %+v landing into main", got)
	}
	if got := declared(recorded, onto("synthetic-other")); got.Into != "" {
		t.Errorf("declared() = %+v landing somewhere it is not declared to go", got)
	}
}

func TestTheDeclaredMethodIsParsedFromTheRecord(t *testing.T) {
	method, refused := declaredMethod(graph.Declaration{Into: "main", By: "rebase"})
	if refused.Reason != "" || method != githubstack.MethodRebase {
		t.Errorf("method = %s, refused = %q; want the declared rebase", method, refused.Reason)
	}
	if _, refused := declaredMethod(graph.Declaration{Into: "main", By: "synthetic-nonsense"}); !strings.Contains(refused.Sentence(), "g2g track --as-trunk") {
		t.Errorf("an unusable declared method was not refused with the way to fix it: %q", refused.Sentence())
	}
}

// What sits on the trunk would have nowhere to land once it has gone, and a
// trunk landing into it would lose the branch it lands into.
func TestADeclaredTrunkWithSomethingOnItIsRefused(t *testing.T) {
	recorded := declaredGraph(t)
	if note := declaredRefusal(recorded, "synthetic-feature"); !strings.Contains(note.Sentence(), "g2g land --branch synthetic-a1") {
		t.Errorf("declaredRefusal() = %q, want the branch on it named", note.Sentence())
	}

	recorded = recorded.Untrack("synthetic-a1")
	if note := declaredRefusal(recorded, "synthetic-feature"); note.Reason != "" {
		t.Errorf("declaredRefusal() = %q with nothing on it", note.Sentence())
	}
	recorded, err := recorded.Declare("synthetic-sub", graph.Declaration{Into: "synthetic-feature", By: "merge"})
	if err != nil {
		t.Fatalf("Declare() error = %v", err)
	}
	if note := declaredRefusal(recorded, "synthetic-feature"); !strings.Contains(note.Reason, "synthetic-sub") {
		t.Errorf("declaredRefusal() = %q, want the trunk landing into it named", note.Sentence())
	}
}

// declaredWorld is newWorld with synthetic-one declared a trunk that lands into
// synthetic-main, landed on its own. synthetic-two still sits on it unless the
// test takes it off.
func declaredWorld(t *testing.T, by string) *world {
	t.Helper()
	w := newWorld(t)
	declared, err := w.store.graph.Clone().Untrack("synthetic-one").Declare("synthetic-one", graph.Declaration{Into: "synthetic-main", By: by})
	if err != nil {
		t.Fatalf("Declare() error = %v", err)
	}
	w.store.graph = declared
	w.service.Selector = fakeSelector{snapshot: stack.Snapshot{
		Target:       "synthetic-one",
		TargetSource: "--branch",
		Ancestry:     []string{"synthetic-main", "synthetic-one"},
		Base:         "synthetic-main",
		Branches:     []string{"synthetic-one"},
		Scope:        shape.ScopePath,
		Source:       stack.SourceG2G,
	}}
	return w
}

// A shared trunk with a stack still on it is refused before anything moves:
// merging it would leave that stack nowhere to land.
func TestPlanRefusesADeclaredTrunkWithAStackOnIt(t *testing.T) {
	w := declaredWorld(t, "rebase")

	plan, err := w.service.Plan(context.Background(), stack.Selection{Branch: "synthetic-one"}, Defaults())
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if !strings.Contains(plan.Blocked, "g2g land --branch synthetic-two") {
		t.Errorf("Blocked = %q, want the branch on it named", plan.Blocked)
	}
	if len(w.events.seen) != 0 {
		t.Errorf("a refused plan did %v", w.events.seen)
	}
}

func TestPlanRefusesAnUnusableDeclaredMethod(t *testing.T) {
	w := declaredWorld(t, "synthetic-nonsense")
	w.store.graph = w.store.graph.Untrack("synthetic-two")

	plan, err := w.service.Plan(context.Background(), stack.Selection{Branch: "synthetic-one"}, Defaults())
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if !strings.Contains(plan.Blocked, "g2g track --as-trunk") {
		t.Errorf("Blocked = %q, want the way to declare it again", plan.Blocked)
	}
}

// Landing a declared trunk publishes it as a stack of one, merges it the way it
// was declared, and asks sync to advance the base alone — the other stacks on
// that base are not this descent's to replay.
func TestPlanLandsADeclaredTrunkAsAStackOfOne(t *testing.T) {
	w := declaredWorld(t, "rebase")
	w.store.graph = w.store.graph.Untrack("synthetic-two")

	plan, err := w.service.Plan(context.Background(), stack.Selection{Branch: "synthetic-one"}, Defaults())
	if err != nil || plan.Blocked != "" {
		t.Fatalf("Plan() = %q, %v", plan.Blocked, err)
	}
	if plan.Options.Method != githubstack.MethodRebase {
		t.Errorf("Method = %s, want the declared rebase", plan.Options.Method)
	}
	if !slices.Equal(w.pusher.scopes, []shape.Scope{shape.ScopePath}) {
		t.Errorf("push was asked for %v, want the path alone", w.pusher.scopes)
	}
	want := graph.Selection{Branch: "synthetic-main", Scope: graph.ScopeBranch}
	if !slices.Equal(w.syncer.selections, []graph.Selection{want}) {
		t.Errorf("sync was asked for %v, want the base alone", w.syncer.selections)
	}

	chosen := Defaults()
	chosen.Method, chosen.MethodChosen = githubstack.MethodMerge, true
	if plan, _ := w.service.Plan(context.Background(), stack.Selection{Branch: "synthetic-one"}, chosen); plan.Options.Method != githubstack.MethodMerge {
		t.Errorf("Method = %s, want the one the run named", plan.Options.Method)
	}
}

// Another source placing the branch on the same base is not the declaration
// talking, so it gets no declared method.
func TestDeclaredOnlyForAStackG2GDescribed(t *testing.T) {
	recorded := declaredGraph(t)
	discovery := stack.Discovery{Snapshot: stack.Snapshot{Target: "synthetic-feature", Base: "main", Source: stack.SourceGraphite}}
	if got := declared(recorded, discovery); got.Lands() {
		t.Errorf("declared() = %+v for a Graphite-described stack", got)
	}
}

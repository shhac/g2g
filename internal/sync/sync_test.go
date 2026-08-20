package sync

import (
	"context"
	"errors"
	"strings"
	"testing"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/restack"
	"github.com/shhac/g2g/internal/testutil"
)

// fakeGit answers the remote and ancestry questions sync asks. Whether the
// base advances, stays put, or has diverged is a decision matrix, which is
// exactly where spawning a process per case buys nothing.
type fakeGit struct {
	objects   map[string]string
	ancestors map[string][]string
	remoteErr error
	// fastForwardErr makes advancing the base fail, which is the first of the
	// three effectful steps sync performs in order.
	fastForwardErr error

	fetched      []string
	fastForwards []string
	// absentOnRemote marks branches the remote no longer has, which is what a
	// merged and deleted branch looks like.
	absentOnRemote map[string]bool
	reset          []string
	// ownCommits maps a branch to the commits it has that the published
	// version does not, by content.
	ownCommits map[string][]string
}

func (f *fakeGit) Remote(_ context.Context, _ string) error { return f.remoteErr }

// RemoteTips says which branches the remote still has. Everything asked for is
// present unless a case removes it, because a branch being gone is the subject
// of only some of these tests.
func (f *fakeGit) RemoteTips(_ context.Context, _ string, branches []string) (map[string]string, error) {
	gone := make([]string, 0, len(f.absentOnRemote))
	for branch := range f.absentOnRemote {
		gone = append(gone, branch)
	}
	return testutil.RemoteTips(branches, gone...), nil
}

func (f *fakeGit) FetchIsolated(_ context.Context, _ string, branches []string) error {
	f.fetched = append(f.fetched, branches...)
	return nil
}

func (f *fakeGit) FastForward(_ context.Context, branch, _ string) error {
	if f.fastForwardErr != nil {
		return f.fastForwardErr
	}
	f.fastForwards = append(f.fastForwards, branch)
	return nil
}

// ResetBranch takes a published version that supersedes the local one, which
// FastForward would refuse because it is not a descendant.
func (f *fakeGit) ResetBranch(_ context.Context, branch, to string) error {
	f.reset = append(f.reset, branch+"<-"+to)
	return nil
}

// Cherry reports the local commits absent from the published side. An empty
// answer means the published version already has everything by content, which
// is what a branch somebody else rebased and pushed looks like.
func (f *fakeGit) Cherry(_ context.Context, _, head, _ string) (absent, present []string, err error) {
	return f.ownCommits[head], nil, nil
}

func (f *fakeGit) Resolve(_ context.Context, revision string) (string, error) {
	if resolved, listed := f.objects[revision]; listed {
		return resolved, nil
	}
	return "", errors.New("not a commit here")
}

func (f *fakeGit) IsAncestor(_ context.Context, ancestor, descendant string) (bool, error) {
	for _, candidate := range f.ancestors[descendant] {
		if candidate == ancestor {
			return true, nil
		}
	}
	return false, nil
}

type memoryStore struct {
	graph graph.Graph
	// writes records every save, so a test can assert prune did not run.
	writes []graph.Graph
}

func (m *memoryStore) Load(context.Context) (graph.Graph, error) { return m.graph.Clone(), nil }
func (m *memoryStore) Save(_ context.Context, g graph.Graph) error {
	m.writes = append(m.writes, g.Clone())
	m.graph = g.Clone()
	return nil
}
func (m *memoryStore) Path(context.Context) (string, error) { return "/synthetic/graph.json", nil }

func adopted() graph.Graph {
	return graph.Graph{
		Edges: map[string]graph.Edge{
			"synthetic-a": {Parent: "synthetic-trunk", ForkPoint: "trunk-old"},
			"synthetic-b": {Parent: "synthetic-a", ForkPoint: "a-old"},
		},
		Trunks: []string{"synthetic-trunk"},
	}
}

// behindGit is the ordinary case: the remote has moved past the local base.
func behindGit() *fakeGit {
	return &fakeGit{
		objects: map[string]string{
			"synthetic-trunk": "trunk-old",
			"synthetic-a":     "a-old",
			"synthetic-b":     "b-old",
			"trunk-old":       "trunk-old",
			"a-old":           "a-old",
			localgit.IsolatedRef("origin", "synthetic-trunk"): "trunk-new",
		},
		ancestors: map[string][]string{
			localgit.IsolatedRef("origin", "synthetic-trunk"): {"synthetic-trunk", "trunk-old"},
			"synthetic-a": {"trunk-old"},
			"synthetic-b": {"a-old", "trunk-old"},
		},
	}
}

// stubRestacker records what it was asked to replay onto, which is the only
// thing sync decides about the replay.
type stubRestacker struct {
	plan    restack.Plan
	onto    []string
	applied int
	err     error
	// applyErr makes the replay fail, which is the second of sync's three
	// steps and the one that must stop the third from running.
	applyErr error
	// reparented records sync asking for a structural change, which it must
	// never do: it moves contents, not structure.
	reparented bool
}

func (s *stubRestacker) Plan(_ context.Context, _ graph.Selection, onto restack.Onto, _ bool) (restack.Plan, error) {
	s.onto = append(s.onto, onto.Object)
	if onto.Reparents() {
		s.reparented = true
	}
	return s.plan, s.err
}

func (s *stubRestacker) Apply(context.Context, restack.Plan) error {
	s.applied++
	return s.applyErr
}

func (s *stubRestacker) InProgress(context.Context) (bool, error) { return false, nil }

func newService(git *fakeGit, restacker Restacker) (Service, *memoryStore) {
	store := &memoryStore{graph: adopted()}
	if restacker == nil {
		restacker = &stubRestacker{}
	}
	return Service{Git: git, Graph: graph.Service{Git: stubAncestry{git}, Store: store}, Restack: restacker}, store
}

// stubAncestry satisfies the graph's Git boundary from the same answers.
type stubAncestry struct{ git *fakeGit }

func (s stubAncestry) CurrentBranch(context.Context) (string, error) { return "synthetic-b", nil }
func (s stubAncestry) LocalBranches(context.Context) ([]string, error) {
	return []string{"synthetic-trunk", "synthetic-a", "synthetic-b"}, nil
}
func (s stubAncestry) AncestorBranches(context.Context, string) ([]string, error) { return nil, nil }
func (s stubAncestry) Divergence(context.Context, string, string) (int, int, error) {
	return 1, 1, nil
}
func (s stubAncestry) IsAncestor(ctx context.Context, a, d string) (bool, error) {
	return s.git.IsAncestor(ctx, a, d)
}
func (s stubAncestry) Resolve(ctx context.Context, revision string) (string, error) {
	return s.git.Resolve(ctx, revision)
}

// Previewing reaches the network but must cost the repository nothing: the
// fetch writes only into g2g's own namespace.
func TestPlanFetchesButAdvancesNothing(t *testing.T) {
	git := behindGit()
	service, _ := newService(git, nil)

	plan, err := service.Plan(context.Background(), graph.Selection{Branch: "synthetic-b"}, "origin", TakeNothing)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	if len(git.fetched) == 0 {
		t.Error("Plan() did not fetch, so it cannot know where the remote is")
	}
	if len(git.fastForwards) != 0 {
		t.Error("Plan() advanced the base")
	}
	if !plan.Advance {
		t.Error("Advance = false for a base the remote has moved past")
	}
}

// A base that has diverged is not something to merge or reset behind the
// user's back, and nothing later in the sequence is attempted.
func TestPlanRefusesADivergedBase(t *testing.T) {
	git := behindGit()
	git.ancestors[localgit.IsolatedRef("origin", "synthetic-trunk")] = nil
	// Diverged and lossy: the published trunk does not have what this one has.
	// Where it does, the trunk is superseded instead, which is its own test.
	git.ownCommits = map[string][]string{"synthetic-trunk": {"synthetic-commit-only-here"}}
	service, _ := newService(git, nil)

	plan, err := service.Plan(context.Background(), graph.Selection{Branch: "synthetic-b"}, "origin", TakeNothing)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	if !plan.Diverged {
		t.Fatal("Diverged = false for a base that cannot be fast-forwarded")
	}
	if plan.Blocked == "" || !strings.Contains(plan.Blocked, "reconcile") {
		t.Errorf("Blocked = %q, want it to say the user reconciles it", plan.Blocked)
	}
	if len(plan.Restack.Steps) != 0 {
		t.Error("a replay was planned against a base that is not going to move")
	}
}

func TestApplyRefusesABlockedPlan(t *testing.T) {
	git := behindGit()
	git.ancestors[localgit.IsolatedRef("origin", "synthetic-trunk")] = nil
	git.ownCommits = map[string][]string{"synthetic-trunk": {"synthetic-commit-only-here"}}
	service, store := newService(git, nil)
	plan, err := service.Plan(context.Background(), graph.Selection{Branch: "synthetic-b"}, "origin", TakeNothing)
	if err != nil {
		t.Fatal(err)
	}
	before := store.graph.Clone()

	if err := service.Apply(context.Background(), plan); err == nil {
		t.Fatal("Apply() error = nil for a blocked plan")
	}
	if len(git.fastForwards) != 0 {
		t.Error("a blocked plan advanced the base anyway")
	}
	if !store.graph.Equal(before) {
		t.Error("a blocked plan changed the recorded graph")
	}
}

// A base level with its remote needs no fast-forward, and saying so is more
// useful than performing a no-op.
func TestPlanLeavesALevelBaseAlone(t *testing.T) {
	git := behindGit()
	git.objects[localgit.IsolatedRef("origin", "synthetic-trunk")] = "trunk-old"
	service, _ := newService(git, nil)

	plan, err := service.Plan(context.Background(), graph.Selection{Branch: "synthetic-b"}, "origin", TakeNothing)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.Advance || plan.Diverged {
		t.Errorf("Advance = %t, Diverged = %t; want neither for a level base", plan.Advance, plan.Diverged)
	}
}

// A base the remote does not have at all is ordinary for a trunk that was
// never pushed, and must not read as divergence.
func TestPlanToleratesABaseTheRemoteDoesNotHave(t *testing.T) {
	git := behindGit()
	delete(git.objects, localgit.IsolatedRef("origin", "synthetic-trunk"))
	service, _ := newService(git, nil)

	plan, err := service.Plan(context.Background(), graph.Selection{Branch: "synthetic-b"}, "origin", TakeNothing)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.Diverged || plan.Blocked != "" {
		t.Errorf("Diverged = %t, Blocked = %q; want an unpushed base to be ordinary", plan.Diverged, plan.Blocked)
	}
}

func TestPlanRequiresSomethingToSync(t *testing.T) {
	git := behindGit()
	store := &memoryStore{graph: graph.New()}
	service := Service{Git: git, Graph: graph.Service{Git: stubAncestry{git}, Store: store}, Restack: &stubRestacker{}}

	_, err := service.Plan(context.Background(), graph.Selection{Branch: "synthetic-b"}, "origin", TakeNothing)
	if err == nil {
		t.Fatal("Plan() error = nil with no recorded structure")
	}
	if !strings.Contains(err.Error(), "g2g track") {
		t.Errorf("error = %v, want it to name the remedy", err)
	}
}

func TestPlanValidatesTheRemote(t *testing.T) {
	git := behindGit()
	git.remoteErr = errors.New("no such remote")
	service, _ := newService(git, nil)

	if _, err := service.Plan(context.Background(), graph.Selection{Branch: "synthetic-b"}, "nowhere", TakeNothing); err == nil {
		t.Fatal("Plan() error = nil for an unknown remote")
	}
}

func TestUnconfiguredServiceRefuses(t *testing.T) {
	if _, err := (Service{}).Plan(context.Background(), graph.Selection{}, "origin", TakeNothing); err == nil {
		t.Fatal("Plan() error = nil for an unconfigured service")
	}
}

// The order is the whole contract: the base moves before the replay, because
// the replay is planned to land on where the base ends up.
func TestApplyAdvancesTheBaseBeforeReplaying(t *testing.T) {
	git := behindGit()
	restacker := &stubRestacker{plan: restack.Plan{Steps: []restack.Step{{Branch: "synthetic-b"}}}}
	service, _ := newService(git, restacker)
	plan, err := service.Plan(context.Background(), graph.Selection{Branch: "synthetic-b"}, "origin", TakeNothing)
	if err != nil {
		t.Fatal(err)
	}

	if err := service.Apply(context.Background(), plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if len(git.fastForwards) != 1 || git.fastForwards[0] != "synthetic-trunk" {
		t.Errorf("fast-forwards = %v, want the base once", git.fastForwards)
	}
	if restacker.applied != 1 {
		t.Errorf("replays = %d, want one", restacker.applied)
	}
	// The replay must target the fetched ref, because the base branch has not
	// moved at the moment the plan is built.
	if len(restacker.onto) == 0 || restacker.onto[0] != localgit.IsolatedRef("origin", "synthetic-trunk") {
		t.Errorf("replayed onto %v, want the fetched base", restacker.onto)
	}
}

func TestPruneIsSkippedWhenNotAskedFor(t *testing.T) {
	git := behindGit()
	restacker := &stubRestacker{plan: restack.Plan{
		Steps: []restack.Step{{Branch: "synthetic-a", Collapses: true}},
	}}
	service, store := newService(git, restacker)
	plan, err := service.Plan(context.Background(), graph.Selection{Branch: "synthetic-b"}, "origin", TakeNothing)
	if err != nil {
		t.Fatal(err)
	}

	if err := service.Apply(context.Background(), plan); err != nil {
		t.Fatal(err)
	}

	if !store.graph.Tracked("synthetic-a") {
		t.Error("a landed branch was forgotten without being asked for")
	}
}

// A base already level with its remote is left alone rather than moved to
// where it already is.
func TestApplyDoesNotTouchALevelBase(t *testing.T) {
	git := behindGit()
	git.objects[localgit.IsolatedRef("origin", "synthetic-trunk")] = "trunk-old"
	service, _ := newService(git, nil)
	plan, err := service.Plan(context.Background(), graph.Selection{Branch: "synthetic-b"}, "origin", TakeNothing)
	if err != nil {
		t.Fatal(err)
	}

	if err := service.Apply(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if len(git.fastForwards) != 0 {
		t.Errorf("fast-forwards = %v, want none for a level base", git.fastForwards)
	}
}

// sync composes three effectful steps and each can fail. Ordering on the happy
// path was covered; stopping was not, and the step it must stop before —
// prune — edits the recorded graph and unpins fork-point refs.
func TestApplyStopsAtTheFirstStepThatFails(t *testing.T) {
	for name, test := range map[string]struct {
		fastForwardErr error
		applyErr       error
		wantReplayed   int
	}{
		"the base cannot be advanced": {fastForwardErr: errors.New("synthetic fast-forward failure")},
		"the replay fails":            {applyErr: errors.New("synthetic replay failure"), wantReplayed: 1},
	} {
		t.Run(name, func(t *testing.T) {
			git := behindGit()
			git.fastForwardErr = test.fastForwardErr
			restacker := &stubRestacker{
				plan:     restack.Plan{Steps: []restack.Step{{Branch: "synthetic-b"}}},
				applyErr: test.applyErr,
			}
			service, store := newService(git, restacker)
			plan, err := service.Plan(context.Background(), graph.Selection{Branch: "synthetic-b"}, "origin", TakeNothing)
			if err != nil {
				t.Fatalf("Plan() error = %v", err)
			}
			if err := service.Apply(context.Background(), plan); err == nil {
				t.Fatal("Apply() error = nil when a step failed")
			}
			if restacker.applied != test.wantReplayed {
				t.Errorf("replayed %d times, want %d", restacker.applied, test.wantReplayed)
			}
			// sync never writes the graph at all now: forgetting what has
			// landed is prune's job and prune's boundary.
			if len(store.writes) != 0 {
				t.Errorf("sync edited the graph %d times", len(store.writes))
			}
		})
	}
}

// sync mutates, so it must re-discover and compare before it does.
//
// It had no revalidation at all: it was the one mutating command that wrote its
// own preview-and-apply sequence instead of using the shared flow, and the copy
// left this step out. A plan approved by a reader could be acted on some time
// later against a repository that had moved.
func TestRevalidateRefusesAPlanThatMovedUnderneath(t *testing.T) {
	git := behindGit()
	restacker := &stubRestacker{plan: restack.Plan{Steps: []restack.Step{{Branch: "synthetic-b"}}}}
	service, _ := newService(git, restacker)

	preview, err := service.Plan(context.Background(), graph.Selection{Branch: "synthetic-b"}, "origin", TakeNothing)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	// The stack moved: what the reader approved replays one branch, what is
	// there now replays a different one.
	restacker.plan = restack.Plan{Steps: []restack.Step{{Branch: "synthetic-c"}}}

	if _, err := service.Revalidate(context.Background(), graph.Selection{Branch: "synthetic-b"}, "origin", TakeNothing, preview); err == nil {
		t.Fatal("Revalidate() error = nil for a plan that changed underneath")
	}
}

// An unchanged repository revalidates cleanly, so the check refuses drift
// rather than refusing everything.
func TestRevalidateAcceptsAnUnchangedPlan(t *testing.T) {
	git := behindGit()
	restacker := &stubRestacker{plan: restack.Plan{Steps: []restack.Step{{Branch: "synthetic-b"}}}}
	service, _ := newService(git, restacker)

	preview, err := service.Plan(context.Background(), graph.Selection{Branch: "synthetic-b"}, "origin", TakeNothing)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	validated, err := service.Revalidate(context.Background(), graph.Selection{Branch: "synthetic-b"}, "origin", TakeNothing, preview)
	if err != nil {
		t.Fatalf("Revalidate() error = %v", err)
	}
	if !validated.Equal(preview) {
		t.Error("an unchanged repository revalidated to a different plan")
	}
}

// Cherry reports every commit as absent from the trunk, so nothing here reads
// as landed by content unless a case is about that.
func (a stubAncestry) Cherry(_ context.Context, _, head, _ string) (absent, present []string, err error) {
	return testutil.OwnCommits(head), nil, nil
}

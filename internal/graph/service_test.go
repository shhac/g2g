package graph

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// memoryStore is an injected store. Preview/apply sequencing is the thing
// under test here, not the filesystem, and a store that records its writes
// makes "did apply write exactly once" directly assertable.
type memoryStore struct {
	graph  Graph
	writes []Graph
	err    error
}

func (m *memoryStore) Load(context.Context) (Graph, error) {
	if m.err != nil {
		return Graph{}, m.err
	}
	return m.graph.Clone(), nil
}

func (m *memoryStore) Save(_ context.Context, g Graph) error {
	if m.err != nil {
		return m.err
	}
	m.writes = append(m.writes, g.Clone())
	m.graph = g.Clone()
	return nil
}

func (m *memoryStore) Path(context.Context) (string, error) {
	return "/synthetic/repo/.git/g2g/graph.json", nil
}

func newService(t *testing.T, git Ancestry, adopted Graph) (Service, *memoryStore) {
	t.Helper()
	store := &memoryStore{graph: adopted}
	return Service{Git: git, Store: store}, store
}

func stackGit() fakeAncestry {
	return fakeAncestry{
		current: "synthetic-login",
		local:   []string{"synthetic-main", "synthetic-auth", "synthetic-login", "synthetic-session", "synthetic-billing"},
		ancestors: map[string][]string{
			"synthetic-login":   {"synthetic-auth"},
			"synthetic-session": {"synthetic-auth"},
			"synthetic-auth":    {},
		},
		behind: map[string]int{"synthetic-auth..synthetic-login": 1, "synthetic-auth..synthetic-session": 1},
	}
}

func TestDiscoverDefaultsToTheCurrentBranchAndSaysSo(t *testing.T) {
	service, _ := newService(t, stackGit(), forest())

	discovery, err := service.Discover(context.Background(), Selection{})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if discovery.Target != "synthetic-login" || discovery.TargetSource != "current Git branch" {
		t.Errorf("target = %q from %q", discovery.Target, discovery.TargetSource)
	}
	if want := "synthetic-main,synthetic-auth,synthetic-login"; strings.Join(discovery.Branches, ",") != want {
		t.Errorf("Branches = %v, want the default path scope %s", discovery.Branches, want)
	}
}

func TestDiscoverNamesTheFileAnApplyWouldWrite(t *testing.T) {
	service, _ := newService(t, stackGit(), forest())

	discovery, err := service.Discover(context.Background(), Selection{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(discovery.StorePath, "g2g/graph.json") {
		t.Errorf("StorePath = %q", discovery.StorePath)
	}
}

func TestDiscoverRequiresAFullyConfiguredService(t *testing.T) {
	if _, err := (Service{}).Discover(context.Background(), Selection{}); err == nil {
		t.Fatal("Discover() error = nil")
	}
}

func TestDiscoverRejectsAnUnknownScope(t *testing.T) {
	service, _ := newService(t, stackGit(), forest())

	if _, err := service.Discover(context.Background(), Selection{Scope: Scope("everything")}); err == nil {
		t.Fatal("Discover() error = nil")
	}
}

// Choosing a parent for the user is the guess this tool does not make, so a
// bare track previews the candidates and blocks.
func TestPlanTrackWithoutAParentBlocksAndOffersCandidates(t *testing.T) {
	service, store := newService(t, stackGit(), New())

	plan, err := service.PlanTrack(context.Background(), Selection{Branch: "synthetic-login"}, "")
	if err != nil {
		t.Fatalf("PlanTrack() error = %v", err)
	}
	if plan.Blocked == "" {
		t.Error("Blocked = \"\", want a bare track to refuse")
	}
	if names := branchNames(plan.Candidates); names != "synthetic-auth" {
		t.Errorf("Candidates = %s, want the nearest ancestor offered", names)
	}
	if len(store.writes) != 0 {
		t.Error("PlanTrack() wrote to the store")
	}
}

func TestPlanTrackRecordsAnUntrackedParentAsANewRoot(t *testing.T) {
	service, _ := newService(t, stackGit(), New())

	plan, err := service.PlanTrack(context.Background(), Selection{Branch: "synthetic-auth"}, "synthetic-main")
	if err != nil {
		t.Fatalf("PlanTrack() error = %v", err)
	}
	if plan.Blocked != "" {
		t.Fatalf("Blocked = %q", plan.Blocked)
	}
	if plan.NewTrunk != "synthetic-main" {
		t.Errorf("NewTrunk = %q, want synthetic-main", plan.NewTrunk)
	}
	if !plan.Updated.IsTrunk("synthetic-main") {
		t.Error("the new root was not recorded as a trunk; the next branch up could not find it as a candidate")
	}
}

func TestPlanTrackDoesNotReRecordAnExistingTrunk(t *testing.T) {
	service, _ := newService(t, stackGit(), forest())

	plan, err := service.PlanTrack(context.Background(), Selection{Branch: "synthetic-session"}, "synthetic-auth")
	if err != nil {
		t.Fatalf("PlanTrack() error = %v", err)
	}
	if plan.NewTrunk != "" {
		t.Errorf("NewTrunk = %q, want none for an already tracked parent", plan.NewTrunk)
	}
}

func TestPlanTrackBlocksOnAnInvalidParent(t *testing.T) {
	service, _ := newService(t, stackGit(), forest())

	for name, parent := range map[string]string{
		"not a local branch": "synthetic-absent",
		"itself":             "synthetic-login",
	} {
		t.Run(name, func(t *testing.T) {
			plan, err := service.PlanTrack(context.Background(), Selection{Branch: "synthetic-login"}, parent)
			if err != nil {
				t.Fatalf("PlanTrack() error = %v", err)
			}
			if plan.Blocked == "" {
				t.Errorf("Blocked = \"\" for parent %q", parent)
			}
		})
	}
}

func TestPlanTrackBlocksOnACycle(t *testing.T) {
	service, _ := newService(t, stackGit(), forest())

	plan, err := service.PlanTrack(context.Background(), Selection{Branch: "synthetic-auth"}, "synthetic-login")
	if err != nil {
		t.Fatalf("PlanTrack() error = %v", err)
	}
	if !strings.Contains(plan.Blocked, "cycle") {
		t.Errorf("Blocked = %q, want it to name the cycle", plan.Blocked)
	}
}

func TestApplyTrackWritesOnceAndRefusesABlockedPlan(t *testing.T) {
	service, store := newService(t, stackGit(), forest())
	ctx := context.Background()

	blocked, err := service.PlanTrack(ctx, Selection{Branch: "synthetic-login"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ApplyTrack(ctx, blocked); err == nil {
		t.Error("ApplyTrack() error = nil for a blocked plan")
	}
	if len(store.writes) != 0 {
		t.Fatal("ApplyTrack() wrote a plan it had refused")
	}

	ready, err := service.PlanTrack(ctx, Selection{Branch: "synthetic-session"}, "synthetic-billing")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ApplyTrack(ctx, ready); err != nil {
		t.Fatalf("ApplyTrack() error = %v", err)
	}
	if len(store.writes) != 1 {
		t.Fatalf("store writes = %d, want exactly one", len(store.writes))
	}
	if parent, _ := store.writes[0].Parent("synthetic-session"); parent != "synthetic-billing" {
		t.Errorf("written parent = %q", parent)
	}
}

// Untracking a middle branch must report the children it strands rather than
// reparenting them onto the grandparent.
func TestPlanUntrackReportsNewlyOrphanedChildren(t *testing.T) {
	service, _ := newService(t, stackGit(), forest())

	plan, err := service.PlanUntrack(context.Background(), Selection{Branch: "synthetic-auth"})
	if err != nil {
		t.Fatalf("PlanUntrack() error = %v", err)
	}
	if want := "synthetic-auth"; strings.Join(plan.Removed, ",") != want {
		t.Errorf("Removed = %v, want %s", plan.Removed, want)
	}
	if want := "synthetic-login,synthetic-session"; strings.Join(plan.Orphaned, ",") != want {
		t.Errorf("Orphaned = %v, want %s", plan.Orphaned, want)
	}
}

func TestPlanUntrackSubtreeRemovesDescendantsAndStrandsNobody(t *testing.T) {
	service, _ := newService(t, stackGit(), forest())

	plan, err := service.PlanUntrack(context.Background(), Selection{Branch: "synthetic-auth", Scope: ScopeSubtree})
	if err != nil {
		t.Fatalf("PlanUntrack() error = %v", err)
	}
	if want := "synthetic-auth,synthetic-login,synthetic-session"; strings.Join(plan.Removed, ",") != want {
		t.Errorf("Removed = %v, want %s", plan.Removed, want)
	}
	if len(plan.Orphaned) != 0 {
		t.Errorf("Orphaned = %v, want none", plan.Orphaned)
	}
}

func TestApplyUntrackRefusesWhenNothingIsTracked(t *testing.T) {
	service, store := newService(t, stackGit(), New())

	plan, err := service.PlanUntrack(context.Background(), Selection{Branch: "synthetic-login"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ApplyUntrack(context.Background(), plan); err == nil {
		t.Error("ApplyUntrack() error = nil with nothing to remove")
	}
	if len(store.writes) != 0 {
		t.Error("ApplyUntrack() wrote despite having nothing to remove")
	}
}

func TestRevalidateRefusesWhenTheGraphMovedUnderneath(t *testing.T) {
	service, store := newService(t, stackGit(), forest())
	ctx := context.Background()

	preview, err := service.PlanTrack(ctx, Selection{Branch: "synthetic-session"}, "synthetic-billing")
	if err != nil {
		t.Fatal(err)
	}
	// Another worktree adopts an edge between preview and apply.
	store.graph, err = store.graph.Track("synthetic-docs", Edge{Parent: "synthetic-main"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.RevalidateTrack(ctx, Selection{Branch: "synthetic-session"}, "synthetic-billing", preview); err == nil {
		t.Fatal("RevalidateTrack() error = nil after the graph changed")
	}
}

func TestRevalidatePassesWhenNothingMoved(t *testing.T) {
	service, _ := newService(t, stackGit(), forest())
	ctx := context.Background()

	preview, err := service.PlanUntrack(ctx, Selection{Branch: "synthetic-login"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RevalidateUntrack(ctx, Selection{Branch: "synthetic-login"}, preview); err != nil {
		t.Fatalf("RevalidateUntrack() error = %v", err)
	}
}

func TestDiscoveryReportsDriftSeparatelyFromStructure(t *testing.T) {
	git := stackGit()
	// session's parent moved underneath it; billing's parent was deleted.
	git.ancestors["synthetic-session"] = nil
	git.local = []string{"synthetic-auth", "synthetic-login", "synthetic-session", "synthetic-billing"}
	service, _ := newService(t, git, forest())

	discovery, err := service.Discover(context.Background(), Selection{Branch: "synthetic-auth", Scope: ScopeTrunk})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if want := "synthetic-session"; strings.Join(discovery.NeedsRestack(), ",") != want {
		t.Errorf("NeedsRestack() = %v, want %s", discovery.NeedsRestack(), want)
	}
	if want := "synthetic-auth,synthetic-billing"; strings.Join(discovery.MissingParents(), ",") != want {
		t.Errorf("MissingParents() = %v, want %s", discovery.MissingParents(), want)
	}
}

func TestDiscoverPropagatesStoreFailures(t *testing.T) {
	service := Service{Git: stackGit(), Store: &memoryStore{err: errors.New("synthetic store failure")}}

	if _, err := service.Discover(context.Background(), Selection{Branch: "synthetic-login"}); err == nil {
		t.Fatal("Discover() error = nil")
	}
}

// Origin is persisted, so it has to mean something. A parent Git already
// agrees with is confirmed; one it does not is the user asserting a
// relationship the commits do not yet show.
func TestPlanTrackRecordsWhetherGitConfirmsTheEdge(t *testing.T) {
	git := stackGit()
	// auth has no ancestors: its trunk has moved on since it forked.
	service, _ := newService(t, git, New())
	ctx := context.Background()

	confirmed, err := service.PlanTrack(ctx, Selection{Branch: "synthetic-login"}, "synthetic-auth")
	if err != nil {
		t.Fatal(err)
	}
	if got := confirmed.Updated.Edges["synthetic-login"].Origin; got != OriginAncestry {
		t.Errorf("origin = %q, want %q for a parent Git confirms", got, OriginAncestry)
	}

	asserted, err := service.PlanTrack(ctx, Selection{Branch: "synthetic-auth"}, "synthetic-main")
	if err != nil {
		t.Fatal(err)
	}
	if got := asserted.Updated.Edges["synthetic-auth"].Origin; got != OriginUser {
		t.Errorf("origin = %q, want %q for a parent that is not an ancestor", got, OriginUser)
	}
}

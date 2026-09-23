package graph

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// landing is the landing-branch shape recorded as an ordinary stack:
// main ← feature ← a1 ← a2.
func landing() Graph {
	return Graph{
		Edges: map[string]Edge{
			"synthetic-feature": {Parent: "synthetic-main", Origin: OriginAncestry, ForkPoint: "0001"},
			"synthetic-a1":      {Parent: "synthetic-feature", Origin: OriginAncestry, ForkPoint: "0002"},
			"synthetic-a2":      {Parent: "synthetic-a1", Origin: OriginAncestry, ForkPoint: "0003"},
		},
		Trunks: []string{"synthetic-main"},
	}
}

func declaredFeature(t *testing.T) Graph {
	t.Helper()
	declared, err := landing().Declare("synthetic-feature", Declaration{Into: "synthetic-main", By: "rebase"})
	if err != nil {
		t.Fatalf("Declare() error = %v", err)
	}
	return declared
}

// Declaring takes the branch out of the stack below it and leaves what sits on
// it where it is, so every walk from above now stops at it.
func TestDeclareMakesABranchARootWithoutMovingWhatSitsOnIt(t *testing.T) {
	declared := declaredFeature(t)

	if declared.Tracked("synthetic-feature") {
		t.Error("the declared trunk kept its edge, so it is still in the stack below it")
	}
	if !declared.IsTrunk("synthetic-feature") || !declared.IsDeclared("synthetic-feature") {
		t.Errorf("Trunks = %v, Declared = %v; want synthetic-feature in both", declared.Trunks, declared.Declared)
	}
	if path, _ := declared.Path("synthetic-a2"); !slices.Equal(path, []string{"synthetic-feature", "synthetic-a1", "synthetic-a2"}) {
		t.Errorf("Path(synthetic-a2) = %v, want it to start at the declared trunk", path)
	}
	if got := declared.Declared["synthetic-feature"]; got != (Declaration{Into: "synthetic-main", By: "rebase"}) {
		t.Errorf("Declared[synthetic-feature] = %+v", got)
	}
	if landing().IsDeclared("synthetic-feature") {
		t.Error("Declare changed the graph it was called on")
	}
}

func TestDeclareRefusesWhatWouldMakeLandingMeaningless(t *testing.T) {
	for name, declaration := range map[string]Declaration{
		"into itself":           {Into: "synthetic-feature", By: "merge"},
		"into a stacked branch": {Into: "synthetic-a1", By: "merge"},
		"where without how":     {Into: "synthetic-main"},
		"how without where":     {By: "merge"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := landing().Declare("synthetic-feature", declaration); err == nil {
				t.Errorf("Declare(%+v) error = nil", declaration)
			}
		})
	}
}

func TestDeclareRefusesATrunkThatLandsInTheEndIntoItself(t *testing.T) {
	declared := declaredFeature(t)
	declared, err := declared.Declare("synthetic-main", Declaration{Into: "synthetic-staging", By: "merge"})
	if err != nil {
		t.Fatalf("Declare(main into staging) error = %v", err)
	}
	if _, err := declared.Declare("synthetic-staging", Declaration{Into: "synthetic-feature", By: "merge"}); err == nil {
		t.Error("Declare() error = nil for staging → feature → main → staging")
	}
}

// What something lands into must not be stacked, or a restack would move it
// under everyone landing into it — and the rule is what makes the cycle check
// on Into alone complete.
func TestTrackRefusesToStackWhatSomethingLandsInto(t *testing.T) {
	declared := declaredFeature(t)

	if _, err := declared.Track("synthetic-main", Edge{Parent: "synthetic-a2"}); err == nil {
		t.Error("Track() error = nil for a parent under the branch feature lands into")
	}
}

// The graph refuses to give a declared trunk a parent, so no path that records
// edges in bulk can end a declaration by forgetting to check for one.
func TestTrackRefusesADeclaredTrunk(t *testing.T) {
	if _, err := declaredFeature(t).Track("synthetic-feature", Edge{Parent: "synthetic-main"}); err == nil {
		t.Error("Track() error = nil for a declared trunk")
	}
}

// track --parent names the branch, so it is the one way a declaration ends.
func TestPlanTrackNamingADeclaredTrunkEndsTheDeclaration(t *testing.T) {
	declared, err := forest().Untrack("synthetic-auth").Declare("synthetic-auth", Declaration{Into: "synthetic-main", By: "merge"})
	if err != nil {
		t.Fatalf("Declare() error = %v", err)
	}
	service, _ := newService(t, stackGit(), declared)

	plan, err := service.PlanTrack(context.Background(), Selection{Branch: "synthetic-auth"}, "synthetic-main")
	if err != nil || plan.Blocked != "" {
		t.Fatalf("PlanTrack() = %q, %v", plan.Blocked, err)
	}
	if parent, _ := plan.Updated.Parent("synthetic-auth"); parent != "synthetic-main" {
		t.Errorf("parent = %q, want synthetic-main", parent)
	}
	if plan.Updated.IsDeclared("synthetic-auth") || plan.Updated.IsTrunk("synthetic-auth") {
		t.Errorf("Trunks = %v, Declared = %v; a branch with a parent is not a trunk", plan.Updated.Trunks, plan.Updated.Declared)
	}
}

func TestUndeclareStrandsWhatSitsOnIt(t *testing.T) {
	undeclared := declaredFeature(t).Undeclare("synthetic-feature")

	if undeclared.IsTrunk("synthetic-feature") || undeclared.IsDeclared("synthetic-feature") {
		t.Errorf("Trunks = %v, Declared = %v", undeclared.Trunks, undeclared.Declared)
	}
	if orphans := undeclared.Orphans(); !slices.Equal(orphans, []string{"synthetic-a1"}) {
		t.Errorf("Orphans() = %v, want synthetic-a1 reported rather than reparented", orphans)
	}
}

func TestRenameCarriesDeclarationsAndWhereTheyLand(t *testing.T) {
	renamed, err := declaredFeature(t).Rename("synthetic-main", "synthetic-trunk")
	if err != nil {
		t.Fatalf("Rename(main) error = %v", err)
	}
	if into := renamed.Declared["synthetic-feature"].Into; into != "synthetic-trunk" {
		t.Errorf("feature lands into %q after renaming main, want synthetic-trunk", into)
	}
	renamed, err = renamed.Rename("synthetic-feature", "synthetic-integration")
	if err != nil {
		t.Fatalf("Rename(feature) error = %v", err)
	}
	if !renamed.IsDeclared("synthetic-integration") || renamed.IsDeclared("synthetic-feature") {
		t.Errorf("Declared = %v, want the declaration under the new name", renamed.Declared)
	}
}

// A branch named only as somewhere a trunk lands is still recorded: renaming
// another branch onto it would quietly change where that trunk goes.
func TestABranchSomethingLandsIntoIsRecorded(t *testing.T) {
	graph := New()
	graph, err := graph.Declare("synthetic-feature", Declaration{Into: "synthetic-release", By: "merge"})
	if err != nil {
		t.Fatalf("Declare() error = %v", err)
	}
	if !graph.Records("synthetic-release") {
		t.Error("Records(synthetic-release) = false for the branch feature lands into")
	}
	if _, err := graph.Rename("synthetic-other", "synthetic-release"); err == nil {
		t.Error("Rename() onto the branch feature lands into error = nil")
	}
}

func TestSaveThenLoadRoundTripsDeclarations(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()
	declared, err := declaredFeature(t).Declare("synthetic-staging", Declaration{})
	if err != nil {
		t.Fatalf("Declare(staging) error = %v", err)
	}

	if err := store.Save(ctx, declared); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !loaded.Equal(declared) {
		t.Errorf("round trip = %#v, want %#v", loaded, declared)
	}
	if !loaded.IsDeclared("synthetic-staging") {
		t.Error("a trunk declared to land nowhere did not survive the round trip")
	}
}

func TestLoadRejectsADeclarationThatIsNotATrunk(t *testing.T) {
	store, common := newStore(t)
	writeStore(t, common, `{"storeSchemaVersion":1,"trunks":["synthetic-main"],
		"declared":{"synthetic-feature":{"into":"synthetic-main","by":"merge"}},
		"branches":{"synthetic-a1":{"parent":"synthetic-feature"}}}`)

	if _, err := store.Load(context.Background()); err == nil {
		t.Fatal("Load() error = nil for a declared branch missing from the trunk set")
	}
}

// Ancestry puts a declared trunk under the branch it grew from, which is what
// the declaration overruled. Adopting the stack above must not undo it.
func TestPlanStackTreatsADeclaredTrunkAsAConflict(t *testing.T) {
	adopted, err := New().withTrunks("synthetic-trunk").Declare("synthetic-a", Declaration{Into: "synthetic-trunk", By: "merge"})
	if err != nil {
		t.Fatalf("Declare() error = %v", err)
	}
	service, store := adoptionService(t, adopted)

	plan, err := service.PlanStack(context.Background(), Selection{}, "synthetic-trunk")
	if err != nil {
		t.Fatalf("PlanStack() error = %v", err)
	}
	if !slices.Equal(plan.Conflicts, []string{"synthetic-a"}) || plan.Blocked == "" {
		t.Fatalf("Conflicts = %v, Blocked = %q; want the declared trunk refused", plan.Conflicts, plan.Blocked)
	}
	if err := service.ApplyStack(context.Background(), plan); err == nil {
		t.Error("ApplyStack() error = nil for a blocked plan")
	}
	if !store.graph.IsDeclared("synthetic-a") {
		t.Error("the declaration did not survive")
	}
}

// Declaring a stacked branch drops its edge, so the fork point it kept alive
// goes too — and a failure to drop it puts the graph back.
func TestApplyDeclareDropsTheReplacedPinOrPutsTheGraphBack(t *testing.T) {
	ctx := context.Background()
	service, store := newService(t, stackGit(), forest())
	pinner := &memoryPinner{pins: map[string]string{"synthetic-auth": "synthetic-auth-fork"}}
	service.Refs = pinner
	declaration := Declaration{Into: "synthetic-main", By: "merge"}

	plan, err := service.PlanDeclare(ctx, Selection{Branch: "synthetic-auth"}, declaration)
	if err != nil || plan.Blocked != "" || plan.Removed != "synthetic-main" {
		t.Fatalf("PlanDeclare() = %+v, %v", plan, err)
	}
	if err := service.ApplyDeclare(ctx, plan); err != nil {
		t.Fatalf("ApplyDeclare() error = %v", err)
	}
	if _, pinned := pinner.pins["synthetic-auth"]; pinned {
		t.Error("the declared trunk's old fork point is still pinned")
	}

	service, store = newService(t, stackGit(), forest())
	service.Refs = &memoryPinner{pins: map[string]string{"synthetic-auth": "synthetic-auth-fork"}, failUnpin: "synthetic-auth"}
	plan, _ = service.PlanDeclare(ctx, Selection{Branch: "synthetic-auth"}, declaration)
	if err := service.ApplyDeclare(ctx, plan); err == nil {
		t.Fatal("ApplyDeclare() error = nil when the pin could not be dropped")
	}
	if !store.graph.Equal(forest()) {
		t.Errorf("graph = %#v, want the graph from before the failed unpin", store.graph)
	}
}

func TestPlanDeclare(t *testing.T) {
	ctx := context.Background()
	landsIntoMain := Declaration{Into: "synthetic-main", By: "merge"}
	declaredAuth, err := forest().Untrack("synthetic-auth").Declare("synthetic-auth", landsIntoMain)
	if err != nil {
		t.Fatalf("Declare() error = %v", err)
	}

	t.Run("somewhere that is not a local branch", func(t *testing.T) {
		service, _ := newService(t, stackGit(), forest())
		plan, err := service.PlanDeclare(ctx, Selection{Branch: "synthetic-auth"}, Declaration{Into: "synthetic-gone", By: "merge"})
		if err != nil || !strings.Contains(plan.Blocked, "not a local branch") {
			t.Errorf("PlanDeclare() = %q, %v", plan.Blocked, err)
		}
	})
	t.Run("a refusal changes nothing", func(t *testing.T) {
		service, _ := newService(t, stackGit(), forest())
		plan, _ := service.PlanDeclare(ctx, Selection{Branch: "synthetic-auth"}, Declaration{Into: "synthetic-login", By: "merge"})
		if plan.Blocked == "" || !plan.Updated.Equal(plan.Graph) || plan.NoOp() {
			t.Errorf("Blocked = %q, Updated changed = %v, NoOp = %v", plan.Blocked, !plan.Updated.Equal(plan.Graph), plan.NoOp())
		}
	})
	t.Run("the same declaration again", func(t *testing.T) {
		service, _ := newService(t, stackGit(), declaredAuth)
		plan, _ := service.PlanDeclare(ctx, Selection{Branch: "synthetic-auth"}, landsIntoMain)
		if !plan.NoOp() {
			t.Errorf("NoOp() = false for a declaration the graph already records")
		}
	})
	t.Run("dropping where it lands is said", func(t *testing.T) {
		service, _ := newService(t, stackGit(), declaredAuth)
		plan, _ := service.PlanDeclare(ctx, Selection{Branch: "synthetic-auth"}, Declaration{})
		if previous, replaced := plan.Replaces(); !replaced || previous != landsIntoMain {
			t.Errorf("Replaces() = %+v, %v; want the landing it drops", previous, replaced)
		}
	})
	t.Run("revalidation refuses a graph that moved", func(t *testing.T) {
		service, store := newService(t, stackGit(), forest())
		preview, _ := service.PlanDeclare(ctx, Selection{Branch: "synthetic-auth"}, landsIntoMain)
		store.graph = store.graph.Untrack("synthetic-billing")
		if _, err := service.RevalidateDeclare(ctx, Selection{Branch: "synthetic-auth"}, landsIntoMain, preview); err == nil {
			t.Error("RevalidateDeclare() error = nil after the graph moved")
		}
	})
}

// Untracking a trunk that another declared trunk lands into leaves that one as
// it is, and says so: where it lands is still somewhere, just not a trunk.
func TestPlanUntrackNamesWhatLandsIntoTheTrunkItEnds(t *testing.T) {
	ctx := context.Background()
	adopted, err := forest().Untrack("synthetic-billing").Declare("synthetic-billing", Declaration{})
	if err != nil {
		t.Fatalf("Declare(billing) error = %v", err)
	}
	if adopted, err = adopted.Untrack("synthetic-auth").Declare("synthetic-auth", Declaration{Into: "synthetic-billing", By: "merge"}); err != nil {
		t.Fatalf("Declare(auth) error = %v", err)
	}
	service, store := newService(t, stackGit(), adopted)

	plan, err := service.PlanUntrack(ctx, Selection{Branch: "synthetic-billing"})
	if err != nil {
		t.Fatalf("PlanUntrack() error = %v", err)
	}
	if !slices.Equal(plan.Undeclared, []string{"synthetic-billing"}) || !slices.Equal(plan.Dependents, []string{"synthetic-auth"}) {
		t.Errorf("Undeclared = %v, Dependents = %v", plan.Undeclared, plan.Dependents)
	}
	if err := service.ApplyUntrack(ctx, plan); err != nil {
		t.Fatalf("ApplyUntrack() error = %v", err)
	}
	if !store.graph.IsDeclared("synthetic-auth") || store.graph.IsTrunk("synthetic-billing") {
		t.Errorf("Declared = %v, Trunks = %v", store.graph.Declared, store.graph.Trunks)
	}
}

package restack

import (
	"context"
	"strings"
	"testing"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/graph"
)

// mergeCarrying is synthetic-main ← synthetic-parent ← synthetic-child, where
// synthetic-child merged a branch nothing records. It returns the merge.
func mergeCarrying(t *testing.T) (realStack, string) {
	t.Helper()
	r := newRealStack(t)
	r.branch("synthetic-parent", "synthetic-main", "parent.txt", "parent")
	r.branch("synthetic-child", "synthetic-parent", "child.txt", "child")
	r.Run("switch", "-qc", "synthetic-dep", "synthetic-main")
	r.Commit("synthetic dep", "dep.txt", "dep")
	r.Run("switch", "-q", "synthetic-child")
	r.Run("merge", "-q", "--no-edit", "synthetic-dep")
	return r, r.Revision("synthetic-child")
}

// The parent of the merge-carrying branch is itself replayed by this plan, so
// merging it in as it is now would leave the branch holding commits the parent
// is about to lose. The refusal names bringing the parent up to date first,
// and moves nothing: not the branch, and not the parent it could have replayed.
func TestRestackRefusesAMergeAboveAParentItWouldMove(t *testing.T) {
	r, merge := mergeCarrying(t)
	r.Run("switch", "-q", "synthetic-main")
	r.Commit("synthetic main moves", "main.txt", "main")
	parent, child := r.Revision("synthetic-parent"), r.Revision("synthetic-child")

	plan, err := r.service.Plan(context.Background(), graph.Selection{Branch: "synthetic-child", Scope: graph.ScopeStack}, Onto{}, false, Pending{}, "")
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if len(plan.Repair.Ways) != 3 || !strings.Contains(plan.Repair.Reason, "merge commit "+localgit.Short(merge)) {
		t.Fatalf("Repair = %+v, want the merge named and three ways out", plan.Repair)
	}
	for index, want := range []string{
		"g2g restack --branch synthetic-parent --scope path",
		"git merge synthetic-parent",
		"g2g track --branch synthetic-child --parent synthetic-parent",
	} {
		if got := plan.Repair.Ways[index].Command; got != want {
			t.Errorf("way %d = %q, want %q", index, got, want)
		}
	}
	if err := r.service.Apply(context.Background(), plan); err == nil {
		t.Fatal("a refused plan was applied")
	}
	if r.Revision("synthetic-parent") != parent || r.Revision("synthetic-child") != child {
		t.Error("a refused restack moved a branch")
	}
	r.assertClean()
}

// A branch that collapses hands the engines nothing, so a merge among the
// commits it no longer contributes is not a reason to refuse: its ref moves.
func TestRestackMovesAMergeCarryingBranchWhoseWorkHasLanded(t *testing.T) {
	r, _ := mergeCarrying(t)
	r.Run("switch", "-q", "synthetic-parent")
	r.Run("merge", "-q", "--squash", "synthetic-child")
	r.Run("commit", "-qm", "synthetic squash of child")

	plan := r.plan(graph.Selection{Branch: "synthetic-child", Scope: graph.ScopeBranch})
	if plan.Blocked() != "" || len(plan.Steps) != 1 || !plan.Steps[0].Collapses {
		t.Fatalf("plan = %+v, blocked %q, want the branch to collapse", plan.Steps, plan.Blocked())
	}
	if err := r.service.Apply(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if r.Revision("synthetic-child") != r.Revision("synthetic-parent") {
		t.Error("synthetic-child did not move onto synthetic-parent")
	}
}

// A child fast-forwarded along its parent, with nothing above the parent's
// tip moved since, holds that tip already. Only its fork point is behind, so
// recording it is the whole of the work: replaying it onto the commit it sits
// on gave the branch, and everything stacked on it, new ids to publish.
func TestRestackOnlyRecordsABranchAlreadyOnItsParentsTip(t *testing.T) {
	r := newRealStack(t)
	r.branch("synthetic-parent", "synthetic-main", "parent.txt", "parent")
	r.Run("switch", "-qc", "synthetic-child")
	r.track("synthetic-child", "synthetic-parent")
	stale := r.Revision("synthetic-parent")
	r.Run("switch", "-q", "synthetic-parent")
	r.Commit("synthetic fix", "fix.txt", "fixed")
	r.Run("switch", "-q", "synthetic-child")
	r.Run("merge", "-q", "--ff-only", "synthetic-parent")
	r.Commit("synthetic child", "child.txt", "child")
	r.branch("synthetic-top", "synthetic-child", "top.txt", "top")
	child, top, parent := r.Revision("synthetic-child"), r.Revision("synthetic-top"), r.Revision("synthetic-parent")
	ctx := context.Background()

	plan := r.plan(graph.Selection{Branch: "synthetic-child", Scope: graph.ScopeStack})
	want := Seated{Branch: "synthetic-child", Parent: "synthetic-parent", ForkPoint: stale, Base: parent}
	if len(plan.Steps) != 0 || len(plan.Seated) != 1 || plan.Seated[0] != want || plan.Nothing() {
		t.Fatalf("Steps = %+v, Seated = %+v, want only %+v recorded", plan.Steps, plan.Seated, want)
	}
	if err := r.service.Apply(ctx, plan); err != nil {
		t.Fatal(err)
	}
	r.assertClean()
	if r.Revision("synthetic-child") != child || r.Revision("synthetic-top") != top {
		t.Error("a branch already on its parent's tip was rewritten")
	}
	g, err := r.store.Load(ctx)
	if err != nil || g.Edges["synthetic-child"].ForkPoint != parent {
		t.Fatalf("fork point not recorded at the parent's tip: %+v, %v", g, err)
	}
	if next := r.plan(graph.Selection{Branch: "synthetic-child", Scope: graph.ScopeStack}); !next.Nothing() {
		t.Fatalf("recorded branch still has work: %+v, %+v", next.Steps, next.Seated)
	}
}

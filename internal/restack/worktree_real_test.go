package restack

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/testutil"
	"github.com/shhac/g2g/internal/worktree"
)

// The questions here are what Git does to another worktree when its branch
// moves, so they need real worktrees: a fake answers whatever it is asked.

// heldStack is synthetic-main < a < b < c with the trunk moved on, b checked
// out in a second worktree, and this checkout standing on the trunk.
func heldStack(t *testing.T) (realStack, testutil.GitRepo) {
	t.Helper()
	r := newRealStack(t)
	r.branch("synthetic-a", "synthetic-main", "a.txt", "a")
	r.branch("synthetic-b", "synthetic-a", "b.txt", "b")
	r.branch("synthetic-c", "synthetic-b", "c.txt", "c")
	r.Run("switch", "-q", "synthetic-main")
	r.Commit("synthetic trunk moved", "main.txt", "moved")
	return r, r.Worktree("synthetic-b")
}

func (r realStack) planWith(policy worktree.Policy) Plan {
	r.t.Helper()
	plan, err := r.service.Plan(context.Background(), graph.Selection{Branch: "synthetic-a", Scope: graph.ScopeStack}, Onto{}, false, Pending{}, policy)
	if err != nil {
		r.t.Fatalf("Plan() error = %v", err)
	}
	return plan
}

func clean(t *testing.T, repo testutil.GitRepo) {
	t.Helper()
	if status := repo.Run("status", "--porcelain"); status != "" {
		t.Errorf("%s is not clean:\n%s", repo.Dir, status)
	}
}

func TestFollowMovesABranchAnotherCleanWorktreeHoldsAndBringsItAlong(t *testing.T) {
	r, held := heldStack(t)

	plan := r.planWith(worktree.Follow)
	if plan.Blocked() != "" {
		t.Fatalf("Blocked = %q, want the clean worktree followed", plan.Blocked())
	}
	if _, follows := plan.Worktrees.Follows("synthetic-b"); !follows || len(plan.Worktrees.Leave) != 0 {
		t.Fatalf("Worktrees = %+v, want synthetic-b followed", plan.Worktrees)
	}
	if err := r.service.Apply(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	for _, branch := range []string{"synthetic-a", "synthetic-b", "synthetic-c"} {
		if r.Try("merge-base", "--is-ancestor", "synthetic-main", branch) != nil {
			t.Errorf("%s was not replayed onto the moved trunk", branch)
		}
	}
	clean(t, held)
	if _, err := os.Stat(filepath.Join(held.Dir, "main.txt")); err != nil {
		t.Errorf("the followed worktree's files are not at its branch's new tip: %v", err)
	}
	if active, _ := r.service.InProgress(context.Background()); active {
		t.Error("the journal outlived a restack that finished")
	}
}

// A worktree with changes is somebody's work in progress. Its branch stays
// where it is, the rest of the stack moves, and what sits on it is measured
// against where it actually is.
func TestFollowLeavesABranchWhoseWorktreeHasChanges(t *testing.T) {
	r, held := heldStack(t)
	held.Write("b.txt", "work in progress")
	before := r.Revision("synthetic-b")
	beforeC := r.Revision("synthetic-c")

	plan := r.planWith(worktree.Follow)
	if plan.Blocked() != "" {
		t.Fatalf("Blocked = %q, want the dirty worktree's branch left", plan.Blocked())
	}
	if !plan.Worktrees.Leaves("synthetic-b") || len(plan.Worktrees.Follow) != 0 {
		t.Fatalf("Worktrees = %+v, want synthetic-b left", plan.Worktrees)
	}
	if strings.Join(plan.Branches(), ",") != "synthetic-a" {
		t.Fatalf("rewrites %v, want only synthetic-a: c already sits on b, which stays", plan.Branches())
	}
	if err := r.service.Apply(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if r.Revision("synthetic-b") != before || r.Revision("synthetic-c") != beforeC {
		t.Error("a branch held by a worktree with changes moved, or what sits on it did")
	}
	if status := held.Run("status", "--porcelain"); status != "M b.txt" {
		t.Errorf("status there = %q, want only the change that was already there", status)
	}
}

// The case that was asked for: b stays, and c, which is behind b, is brought
// up to b where it is now. A restack run from b's worktree later moves both.
func TestALeftBranchStillHasWhatSitsOnItRestackedOntoIt(t *testing.T) {
	r, held := heldStack(t)
	held.Commit("synthetic b gained a commit", "b2.txt", "b2")
	held.Write("b.txt", "work in progress")
	tip := r.Revision("synthetic-b")

	plan := r.planWith(worktree.Follow)
	if strings.Join(plan.Branches(), ",") != "synthetic-a,synthetic-c" {
		t.Fatalf("rewrites %v, want synthetic-a and synthetic-c", plan.Branches())
	}
	if err := r.service.Apply(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if r.Revision("synthetic-b") != tip {
		t.Error("synthetic-b moved")
	}
	if r.Try("merge-base", "--is-ancestor", tip, "synthetic-c") != nil {
		t.Error("synthetic-c was not restacked onto synthetic-b's current tip")
	}
	if r.Try("merge-base", "--is-ancestor", "synthetic-main", "synthetic-c") == nil {
		t.Error("synthetic-c reached the new trunk through a parent that stayed behind")
	}
}

func TestSkipLeavesEvenACleanWorktreesBranch(t *testing.T) {
	r, held := heldStack(t)
	before := r.Revision("synthetic-b")

	plan := r.planWith(worktree.Skip)
	if !plan.Worktrees.Leaves("synthetic-b") {
		t.Fatalf("Worktrees = %+v, want synthetic-b left", plan.Worktrees)
	}
	if err := r.service.Apply(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if r.Revision("synthetic-b") != before {
		t.Error("skip moved a held branch")
	}
	clean(t, held)
}

func TestRefuseStillRefuses(t *testing.T) {
	r, _ := heldStack(t)
	for _, policy := range []worktree.Policy{worktree.Refuse, ""} {
		plan := r.planWith(policy)
		if !plan.Held || !strings.Contains(plan.Blocked(), "another worktree") {
			t.Errorf("%q: Blocked = %q, want the held branch refused", policy, plan.Blocked())
		}
	}
}

// Clean at the preview and dirty by the apply is a different plan, and the
// apply's revalidation compares plans: the branch went from followed to left.
func TestAWorktreeThatPicksUpChangesChangesThePlan(t *testing.T) {
	r, held := heldStack(t)
	preview := r.planWith(worktree.Follow)
	held.Write("b.txt", "work in progress")
	if preview.Equal(r.planWith(worktree.Follow)) {
		t.Error("the plan did not change when the followed worktree picked up changes")
	}
}

// A rewrite that has to rebase checks each branch out here, which Git refuses
// for a branch another worktree has; following cannot help.
func TestFollowRefusesARewriteThatWouldHaveToCheckTheBranchOutHere(t *testing.T) {
	r := newRealStack(t)
	r.branch("synthetic-a", "synthetic-main", "shared.txt", "a")
	r.Run("switch", "-q", "synthetic-main")
	r.Commit("synthetic trunk conflicts", "shared.txt", "main")
	r.Worktree("synthetic-a")

	plan, err := r.service.Plan(context.Background(), graph.Selection{Branch: "synthetic-a", Scope: graph.ScopeStack}, Onto{}, false, Pending{}, worktree.Follow)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Held || !strings.Contains(plan.Blocked(), "has to run in a working tree") {
		t.Fatalf("Blocked = %q, want a refusal naming the worktree", plan.Blocked())
	}
}

// A restack that dies after moving a followed branch and before bringing its
// worktree along leaves the journal saying so, and --continue finishes it.
func TestContinueBringsAlongAWorktreeARestackLeftBehind(t *testing.T) {
	r, held := heldStack(t)
	ctx := context.Background()
	plan := r.planWith(worktree.Follow)
	record, err := r.service.begin(ctx, plan, checkout{})
	if err != nil {
		t.Fatal(err)
	}
	if record.Followed["synthetic-b"].Tip == "" {
		t.Fatalf("Followed = %+v, want synthetic-b noted before anything moves", record.Followed)
	}
	if err := r.service.replay(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if held.Run("status", "--porcelain") == "" {
		t.Fatal("the replay did not strand the worktree, so this proves nothing")
	}

	if err := r.service.Continue(ctx); err != nil {
		t.Fatal(err)
	}
	clean(t, held)
	if active, _ := r.service.InProgress(ctx); active {
		t.Error("the journal outlived the continue")
	}
}

// stranded is a restack that died after moving a followed branch and bringing
// its worktree along, with the journal still there.
func stranded(t *testing.T) (realStack, testutil.GitRepo, string) {
	t.Helper()
	r, held := heldStack(t)
	ctx := context.Background()
	original := r.Revision("synthetic-b")
	plan := r.planWith(worktree.Follow)
	record, err := r.service.begin(ctx, plan, checkout{})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.service.replay(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if err := r.service.settleFollowed(ctx, &record); err != nil {
		t.Fatal(err)
	}
	return r, held, original
}

func TestAbortBringsAFollowedWorktreeBack(t *testing.T) {
	r, held, original := stranded(t)
	if err := r.service.Abort(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.Revision("synthetic-b") != original {
		t.Error("synthetic-b was not put back")
	}
	clean(t, held)
	if _, err := os.Stat(filepath.Join(held.Dir, "main.txt")); !os.IsNotExist(err) {
		t.Errorf("the followed worktree's files were not brought back: %v", err)
	}
}

// Somebody committed in the followed worktree after the restack died: an
// abort would reset that commit away, so it refuses as for any held branch.
func TestAbortRefusesAFollowedBranchCommittedToSince(t *testing.T) {
	r, held, _ := stranded(t)
	held.Commit("synthetic work after the restack", "after.txt", "after")
	committed := r.Revision("synthetic-b")
	if err := r.service.Abort(context.Background()); err == nil || !strings.Contains(err.Error(), "another worktree") {
		t.Fatalf("Abort() = %v, want the held branch refused", err)
	}
	if r.Revision("synthetic-b") != committed {
		t.Error("abort reset the commit made in the followed worktree")
	}
}

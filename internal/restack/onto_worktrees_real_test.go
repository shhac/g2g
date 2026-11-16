package restack

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/worktree"
)

func TestOntoBackwardsCollapsePreservesLeftDescendantFork(t *testing.T) {
	ctx := context.Background()
	r := newRealStack(t)
	r.Run("branch", "synthetic-release")
	r.Commit("synthetic trunk advance", "advance.txt", "synthetic advance")
	r.Run("branch", "synthetic-a")
	r.track("synthetic-a", "synthetic-main")
	r.branch("synthetic-b", "synthetic-a", "b.txt", "synthetic b")
	r.Run("switch", "-q", "synthetic-main")
	held := r.Worktree("synthetic-b")
	held.Write("b.txt", "synthetic edits")
	before, err := r.store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tip := r.Revision("synthetic-b")
	plan, err := r.service.Plan(ctx, graph.Selection{Branch: "synthetic-a", Scope: graph.ScopeSubtree}, ToBranch("synthetic-release"), false, Pending{}, worktree.Skip)
	if err != nil || plan.Blocked() != "" {
		t.Fatalf("Plan = %v, %s", err, plan.Blocked())
	}
	if !plan.Worktrees.Leaves("synthetic-b") {
		t.Fatal("synthetic-b was not left")
	}
	if err := r.service.Apply(ctx, plan); err != nil {
		t.Fatal(err)
	}
	after, err := r.store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Edges["synthetic-b"] != before.Edges["synthetic-b"] {
		t.Errorf("left edge = %+v, want %+v; replay range widened", after.Edges["synthetic-b"], before.Edges["synthetic-b"])
	}
	if got := r.Revision("refs/g2g/forkpoints/synthetic-b"); got != before.Edges["synthetic-b"].ForkPoint {
		t.Errorf("pinned fork = %s, want %s", got, before.Edges["synthetic-b"].ForkPoint)
	}
	if r.Revision("synthetic-a") != r.Revision("synthetic-release") || r.Revision("synthetic-b") != tip {
		t.Fatal("collapse or left tip incorrect")
	}
	if held.Run("status", "--porcelain") != "M b.txt" {
		t.Fatal("held edits changed")
	}
	r.assertClean()
}

func TestStructureMovesPreserveLeftBranchesAcrossJournal(t *testing.T) {
	left := []worktree.Holder{{Branch: "synthetic-b", Path: "/synthetic/held"}}
	plan := Plan{Worktrees: worktree.NewOutcome(nil, left)}
	if !plan.structure().left["synthetic-b"] {
		t.Fatal("plan refreshes a left branch")
	}
	record := (Service{}).record(plan, checkout{}, graph.Graph{})
	journal, _ := newJournal(t)
	if err := journal.Save(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := journal.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SchemaVersion != 3 || !loaded.structure().left["synthetic-b"] || !reflect.DeepEqual(loaded.Left, left) {
		t.Fatalf("left branches lost: %+v", loaded)
	}
}

func TestOntoLeftMiddleConflictRecoveryPreservesRanges(t *testing.T) {
	for _, verb := range []string{"continue", "abort"} {
		t.Run(verb, func(t *testing.T) {
			ctx := context.Background()
			r := newRealStack(t)
			r.Commit("synthetic shared base", "shared.txt", "synthetic base")
			r.Run("branch", "synthetic-release")
			r.Commit("synthetic trunk advance", "advance.txt", "synthetic advance")
			r.Run("branch", "synthetic-a")
			r.track("synthetic-a", "synthetic-main")
			r.branch("synthetic-b", "synthetic-a", "b.txt", "synthetic b")
			r.branch("synthetic-c", "synthetic-b", "shared.txt", "synthetic child")
			r.Run("switch", "-q", "synthetic-main")
			held := r.Worktree("synthetic-b")
			held.Commit("synthetic parent advance", "shared.txt", "synthetic parent")
			held.Write("b.txt", "synthetic uncommitted work")
			before, err := r.store.Load(ctx)
			if err != nil {
				t.Fatal(err)
			}
			tips := map[string]string{}
			for _, branch := range []string{"synthetic-a", "synthetic-b", "synthetic-c"} {
				tips[branch] = r.Revision(branch)
			}
			plan, err := r.service.Plan(ctx, graph.Selection{Branch: "synthetic-a", Scope: graph.ScopeSubtree}, ToBranch("synthetic-release"), false, Pending{}, worktree.Skip)
			if err != nil || plan.Blocked() != "" || !plan.Worktrees.Leaves("synthetic-b") {
				t.Fatalf("Plan = %+v, %v", plan, err)
			}
			if err := r.service.Apply(ctx, plan); err == nil {
				t.Fatal("expected child conflict")
			}
			if got := r.Run("diff", "--name-only", "--diff-filter=U"); got != "shared.txt" {
				t.Fatalf("stopped for %q, want real child conflict", got)
			}
			if r.Revision("synthetic-a") != r.Revision("synthetic-release") {
				t.Fatal("root did not collapse before conflict")
			}
			record, found, err := r.service.Journal.Load(ctx)
			if err != nil || !found || !record.structure().left["synthetic-b"] {
				t.Fatalf("left boundary not journaled: %+v, %v", record, err)
			}
			if verb == "continue" {
				r.Write("shared.txt", "synthetic parent and child resolved")
				r.Run("add", "shared.txt")
				// The original leave remains binding after its holder detaches.
				held.Run("switch", "-q", "--detach")
				if err := r.service.Continue(ctx); err != nil {
					t.Fatal(err)
				}
				r.builtOn("synthetic-b", "synthetic-c")
			} else {
				if err := r.service.Abort(ctx); err != nil {
					t.Fatal(err)
				}
				for branch, tip := range tips {
					if r.Revision(branch) != tip {
						t.Errorf("abort moved %s", branch)
					}
				}
			}
			after, err := r.store.Load(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if r.Revision("synthetic-b") != tips["synthetic-b"] || after.Edges["synthetic-b"] != before.Edges["synthetic-b"] {
				t.Fatal("left middle tip or replay boundary changed")
			}
			if verb == "abort" && !reflect.DeepEqual(after, before) {
				t.Fatalf("abort did not restore structure: %+v, want %+v", after, before)
			}
			if verb == "continue" && (after.Edges["synthetic-a"].Parent != "synthetic-release" || after.Edges["synthetic-c"].Parent != "synthetic-b" || after.Edges["synthetic-c"].ForkPoint != tips["synthetic-b"]) {
				t.Fatalf("continued structure = %+v", after)
			}
			for branch, edge := range after.Edges {
				if r.Revision("refs/g2g/forkpoints/"+branch) != edge.ForkPoint {
					t.Errorf("pin for %s disagrees with edge", branch)
				}
			}
			if got, err := os.ReadFile(filepath.Join(held.Dir, "b.txt")); err != nil || string(got) != "synthetic uncommitted work" || held.Run("status", "--porcelain") != "M b.txt" {
				t.Fatal("held worktree edits changed")
			}
			if active, _ := r.service.InProgress(ctx); active {
				t.Fatal("finished recovery retained journal")
			}
			r.assertClean()
		})
	}
}

// Leaving a held middle branch still permits a stale child to replay onto
// its actual tip. --onto reparents only the root below that held branch.
func TestOntoLeavesAHeldDescendantAndRestacksItsStaleChild(t *testing.T) {
	for _, policy := range []worktree.Policy{worktree.Skip, worktree.Follow} {
		t.Run(string(policy), func(t *testing.T) {
			r, held := heldStack(t)
			r.Run("branch", "synthetic-release", "synthetic-main")
			held.Commit("synthetic held branch moved", "synthetic-extra.txt", "synthetic extra")
			held.Write("b.txt", "synthetic work in progress")
			before := r.Revision("synthetic-b")
			ctx := context.Background()
			recorded, err := r.store.Load(ctx)
			if err != nil {
				t.Fatal(err)
			}

			plan, err := r.service.Plan(ctx, graph.Selection{Branch: "synthetic-a", Scope: graph.ScopeStack}, ToBranch("synthetic-release"), false, Pending{}, policy)
			if err != nil || plan.Blocked() != "" {
				t.Fatalf("Plan() = %v, blocked %q", err, plan.Blocked())
			}
			if !plan.Worktrees.Leaves("synthetic-b") || !reflect.DeepEqual(plan.Branches(), []string{"synthetic-a", "synthetic-c"}) {
				t.Fatalf("branches = %v, worktrees = %+v, want only root and stale child moved", plan.Branches(), plan.Worktrees)
			}
			if err := r.service.Apply(ctx, plan); err != nil {
				t.Fatal(err)
			}
			clean(t, r.GitRepo)
			r.builtOn("synthetic-release", "synthetic-a")
			r.builtOn("synthetic-b", "synthetic-c")
			progress, err := os.ReadFile(filepath.Join(held.Dir, "b.txt"))
			if err != nil {
				t.Fatal(err)
			}
			if r.Revision("synthetic-b") != before || held.Run("status", "--porcelain") != "M b.txt" || string(progress) != "synthetic work in progress" {
				t.Error("the held branch or its work in progress changed")
			}
			after, err := r.store.Load(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if after.Edges["synthetic-a"].Parent != "synthetic-release" || after.Edges["synthetic-b"] != recorded.Edges["synthetic-b"] || after.Edges["synthetic-c"].Parent != "synthetic-b" {
				t.Errorf("edges = %+v, want only the root reparented and the held edge unchanged", after.Edges)
			}
		})
	}
}

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

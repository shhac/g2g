package restack

import (
	"context"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/worktree"
)

// Empty branches are ref aliases of their parent, including when a conflict
// makes the rewrite resume through a different engine. Children above an empty
// branch must inherit the replayed parent too.
func TestEmptyBranchesFollowTheirReplayedParents(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(map[bool]string{false: "clean", true: "conflict-continue"}[conflict], func(t *testing.T) {
			r := newRealStack(t)
			r.branch("synthetic-parent", "synthetic-main", "root.txt", "parent")
			r.Run("switch", "-qc", "synthetic-empty")
			r.track("synthetic-empty", "synthetic-parent")
			r.branch("synthetic-child", "synthetic-empty", "child.txt", "child")
			r.Run("switch", "-qc", "synthetic-top")
			r.track("synthetic-top", "synthetic-child")
			r.Run("switch", "-q", "synthetic-main")
			file := "trunk.txt"
			if conflict {
				file = "root.txt"
			}
			r.Commit("synthetic trunk advance", file, "trunk")
			r.Run("switch", "-q", "synthetic-top")
			plan := r.plan(graph.Selection{Branch: "synthetic-top", Scope: graph.ScopeStack})
			err := r.service.Apply(context.Background(), plan)
			if conflict {
				if err == nil {
					t.Fatal("rewrite did not stop on the intended conflict")
				}
				r.Write("root.txt", "resolved")
				r.Run("add", "root.txt")
				err = r.service.Continue(context.Background())
			}
			if err != nil {
				t.Fatal(err)
			}
			for empty, parent := range map[string]string{"synthetic-empty": "synthetic-parent", "synthetic-top": "synthetic-child"} {
				if r.Revision(empty) != r.Revision(parent) {
					t.Errorf("%s was left behind %s", empty, parent)
				}
			}
			r.builtOn("synthetic-main", "synthetic-parent")
			r.builtOn("synthetic-empty", "synthetic-child")
			r.assertClean()
			if got := r.Run("branch", "--show-current"); got != "synthetic-top" {
				t.Errorf("checkout = %q, want synthetic-top", got)
			}
			if active, err := r.service.InProgress(context.Background()); err != nil || active {
				t.Fatalf("journal active = %v, %v", active, err)
			}
		})
	}
}

func TestEmptyBranchHeldElsewhereIsFollowedOrLeftBeforeItsParentReplays(t *testing.T) {
	for _, dirty := range []bool{false, true} {
		t.Run(map[bool]string{false: "clean", true: "dirty"}[dirty], func(t *testing.T) {
			r := newRealStack(t)
			r.branch("synthetic-parent", "synthetic-main", "parent.txt", "parent")
			r.Run("switch", "-qc", "synthetic-empty")
			r.track("synthetic-empty", "synthetic-parent")
			before := r.Revision("synthetic-empty")
			r.Run("switch", "-q", "synthetic-main")
			r.Commit("synthetic trunk advance", "trunk.txt", "trunk")
			held := r.Worktree("synthetic-empty")
			if dirty {
				held.Write("parent.txt", "unfinished")
			}
			plan, err := r.service.Plan(context.Background(), graph.Selection{Branch: "synthetic-parent", Scope: graph.ScopeStack}, Onto{}, false, Pending{}, worktree.Follow)
			if err != nil || plan.Blocked() != "" {
				t.Fatalf("plan: %v, %s", err, plan.Blocked())
			}
			if err := r.service.Apply(context.Background(), plan); err != nil {
				t.Fatal(err)
			}
			if dirty {
				if !plan.Worktrees.Leaves("synthetic-empty") || r.Revision("synthetic-empty") != before {
					t.Fatal("dirty empty branch was moved")
				}
				if got := held.Run("diff", "--", "parent.txt"); !strings.Contains(got, "+unfinished") {
					t.Fatalf("dirty work changed: %s", got)
				}
			} else {
				if _, follows := plan.Worktrees.Follows("synthetic-empty"); !follows || r.Revision("synthetic-empty") != r.Revision("synthetic-parent") {
					t.Fatal("clean empty branch did not follow its replayed parent")
				}
				clean(t, held)
			}
		})
	}
}

func TestAbortRestoresEmptyBranchesMovedAfterTheirParentReplay(t *testing.T) {
	r := newRealStack(t)
	r.branch("synthetic-parent", "synthetic-main", "parent.txt", "parent")
	r.Run("switch", "-qc", "synthetic-empty")
	r.track("synthetic-empty", "synthetic-parent")
	r.branch("synthetic-child", "synthetic-empty", "root.txt", "child")
	before := map[string]string{}
	for _, branch := range []string{"synthetic-parent", "synthetic-empty", "synthetic-child"} {
		before[branch] = r.Revision(branch)
	}
	r.Run("switch", "-q", "synthetic-main")
	r.Commit("synthetic trunk advance", "root.txt", "trunk")
	r.Run("switch", "-q", "synthetic-child")
	plan := r.plan(graph.Selection{Branch: "synthetic-child", Scope: graph.ScopeStack})
	if err := r.service.Apply(context.Background(), plan); err == nil {
		t.Fatal("rewrite did not stop on the child conflict")
	}
	if r.Revision("synthetic-empty") != r.Revision("synthetic-parent") || r.Revision("synthetic-empty") == before["synthetic-empty"] {
		t.Fatal("empty branch did not follow the completed parent replay")
	}
	if err := r.service.Abort(context.Background()); err != nil {
		t.Fatal(err)
	}
	for branch, tip := range before {
		if r.Revision(branch) != tip {
			t.Errorf("abort did not restore %s", branch)
		}
	}
	r.assertClean()
}

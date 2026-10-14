package restack

import (
	"context"
	"testing"

	"github.com/shhac/g2g/internal/graph"
)

// forkedConflict is main < A < {B, C}, with the trunk then changing what both
// B and C change, so replaying the stack conflicts on each line of descent.
func forkedConflict(t *testing.T) realStack {
	t.Helper()
	r := newRealStack(t)
	r.Commit("synthetic shared", "b.txt", "base")
	r.Commit("synthetic shared too", "c.txt", "base")
	r.branch("synthetic-a", "synthetic-main", "a.txt", "from a")
	r.branch("synthetic-b", "synthetic-a", "b.txt", "from b")
	r.branch("synthetic-c", "synthetic-a", "c.txt", "from c")
	r.Run("switch", "-q", "synthetic-main")
	r.Write("b.txt", "from the trunk")
	r.Write("c.txt", "from the trunk")
	r.Run("commit", "-qam", "synthetic trunk change")
	return r
}

// A rewrite that conflicts on two lines of descent stops on the first, and
// each --continue goes on to what is left -- the other line included --
// because what is left is worked out again from the refs every time.
func TestAForkedRewriteThatConflictsStopsOnEachLineAndContinues(t *testing.T) {
	r := forkedConflict(t)
	ctx := context.Background()
	r.stopOnConflict(graph.Selection{Branch: "synthetic-a", Scope: graph.ScopeStack})
	for stops := 0; ; stops++ {
		if stops == 3 {
			t.Fatal("the restack kept stopping")
		}
		for _, name := range []string{"b.txt", "c.txt"} {
			if r.Try("diff", "--quiet", "--diff-filter=U", "--", name) != nil {
				r.Write(name, "resolved")
				r.Run("add", name)
			}
		}
		if err := r.service.Continue(ctx); err == nil {
			if stops != 1 {
				t.Errorf("finished after %d continues, want a stop on each line", stops+1)
			}
			break
		}
	}
	r.builtOn("synthetic-main", "synthetic-a")
	r.builtOn("synthetic-a", "synthetic-b")
	r.builtOn("synthetic-a", "synthetic-c")
	for branch, file := range map[string]string{"synthetic-b": "b.txt", "synthetic-c": "c.txt"} {
		if got := r.Run("show", branch+":"+file); got != "resolved" {
			t.Errorf("%s:%s = %q, want the resolution", branch, file, got)
		}
	}
	if active, _ := r.service.InProgress(ctx); active {
		t.Error("a finished restack kept its journal")
	}
	r.assertClean()
}

// Abort part-way across a fork puts back every branch -- the line already
// finished and the one stopped on -- as it does for a single line.
func TestAbortingAForkedRewriteRestoresEveryLine(t *testing.T) {
	r := forkedConflict(t)
	ctx := context.Background()
	before := map[string]string{}
	for _, branch := range []string{"synthetic-a", "synthetic-b", "synthetic-c"} {
		before[branch] = r.Revision(branch)
	}
	r.stopOnConflict(graph.Selection{Branch: "synthetic-a", Scope: graph.ScopeStack})
	r.Write("b.txt", "resolved")
	r.Run("add", "b.txt")
	if err := r.service.Continue(ctx); err == nil {
		t.Fatal("Continue() finished, want a stop on the second line")
	}
	if err := r.service.Abort(ctx); err != nil {
		t.Fatalf("Abort() error = %v", err)
	}
	for branch, tip := range before {
		if got := r.Revision(branch); got != tip {
			t.Errorf("%s = %s after abort, want %s", branch, got, tip)
		}
	}
	if active, _ := r.service.InProgress(ctx); active {
		t.Error("an aborted restack kept its journal")
	}
	r.assertClean()
}

// Skipping part-way across a fork drops the commit stopped on and carries on
// to the other line, as continuing does: what is left is worked out from the
// refs, whichever verb resumed it.
func TestSkippingAcrossAForkedRewriteReachesTheOtherLine(t *testing.T) {
	r := forkedConflict(t)
	ctx := context.Background()
	r.stopOnConflict(graph.Selection{Branch: "synthetic-a", Scope: graph.ScopeStack})
	skips := 0
	for ; skips < 3; skips++ {
		if err := r.service.Skip(ctx); err == nil {
			break
		}
	}
	if skips != 1 {
		t.Errorf("finished after %d skips, want a stop on each line", skips+1)
	}
	r.builtOn("synthetic-main", "synthetic-a")
	for branch, file := range map[string]string{"synthetic-b": "b.txt", "synthetic-c": "c.txt"} {
		if r.Try("merge-base", "--is-ancestor", "synthetic-a", branch) != nil {
			t.Errorf("%s is not built on synthetic-a", branch)
		}
		if got := r.Run("show", branch+":"+file); got != "from the trunk" {
			t.Errorf("%s:%s = %q, want the trunk's, its own commit skipped", branch, file, got)
		}
	}
	if active, _ := r.service.InProgress(ctx); active {
		t.Error("a finished restack kept its journal")
	}
	r.assertClean()
}

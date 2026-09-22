package stack

import (
	"context"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
)

// A tracked branch deleted with plain Git, followed through every command that
// selects a stack — status first among them.
//
// The selection used to go through untouched and hand the deleted name onward,
// so status either failed on Git's "Not a valid object name" or, where nothing
// happened to ask Git about it, advised opening a pull request for a branch
// that no longer exists. Each step now names the command that repairs it, and
// the repair works: untrack the stale edge, then give the child a parent that
// is here.
func TestADeletedTrackedBranchIsRefusedByNameUntilItIsRepaired(t *testing.T) {
	repo := testutil.NewGitRepo(t, "synthetic-main")
	repo.Commit("synthetic root", "root.txt", "root")
	repo.Run("switch", "-qc", "synthetic-a")
	repo.Commit("synthetic a", "a.txt", "a")
	repo.Run("switch", "-qc", "synthetic-b")
	repo.Commit("synthetic b", "b.txt", "b")

	t.Chdir(repo.Dir)
	ctx := context.Background()
	client := git.Client{Runner: subprocess.ExecRunner{}}
	service := graph.Service{Git: client, Store: graph.FileStore{Git: client}, Refs: client}
	track := func(branch, parent string) {
		t.Helper()
		plan, err := service.PlanTrack(ctx, graph.Selection{Branch: branch}, parent)
		if err != nil {
			t.Fatalf("PlanTrack(%s) error = %v", branch, err)
		}
		if err := service.ApplyTrack(ctx, plan); err != nil {
			t.Fatalf("ApplyTrack(%s) error = %v", branch, err)
		}
	}
	track("synthetic-a", "synthetic-main")
	track("synthetic-b", "synthetic-a")
	selector := G2GSelector{Service: service}

	repo.Run("switch", "-q", "synthetic-main")
	repo.Run("branch", "-qD", "synthetic-a")

	_, err := selector.Select(ctx, Selection{Branch: "synthetic-b"}, "synthetic command")
	if err == nil {
		t.Fatal("Select() accepted a selection containing a branch that no longer exists")
	}
	if !strings.Contains(err.Error(), "g2g untrack --branch synthetic-a") {
		t.Errorf("error = %v, want it to name the untrack that forgets the stale edge", err)
	}
	if strings.Contains(err.Error(), "Not a valid object name") {
		t.Errorf("error = %v, want a sentence rather than Git's", err)
	}

	untrack, err := service.PlanUntrack(ctx, graph.Selection{Branch: "synthetic-a", Scope: graph.ScopeBranch})
	if err != nil {
		t.Fatalf("PlanUntrack() error = %v", err)
	}
	if err := service.ApplyUntrack(ctx, untrack); err != nil {
		t.Fatalf("ApplyUntrack() error = %v", err)
	}

	// The edge is gone and the child still names it, which untrack reports
	// rather than repairs. A base that is not here is no place to stand.
	_, err = selector.Select(ctx, Selection{Branch: "synthetic-b"}, "synthetic command")
	if err == nil {
		t.Fatal("Select() hung the stack from a branch that no longer exists")
	}
	if !strings.Contains(err.Error(), "g2g track --parent") {
		t.Errorf("error = %v, want it to name the track that gives synthetic-b a parent", err)
	}

	graphed, err := service.Discover(ctx, graph.Selection{Branch: "synthetic-b"})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if got := graphed.States["synthetic-b"]; got != graph.StateParentMissing {
		t.Errorf("synthetic-b reads as %q, want %q", got, graph.StateParentMissing)
	}

	track("synthetic-b", "synthetic-main")
	snapshot, err := selector.Select(ctx, Selection{Branch: "synthetic-b"}, "synthetic command")
	if err != nil {
		t.Fatalf("Select() after the repair error = %v", err)
	}
	if snapshot.Base != "synthetic-main" || strings.Join(snapshot.Branches, ",") != "synthetic-b" {
		t.Errorf("Select() = %s on %s, want synthetic-b on synthetic-main", strings.Join(snapshot.Branches, ","), snapshot.Base)
	}
}

package graph_test

import (
	"context"
	"testing"

	"github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
)

// stackedPair records synthetic-child on synthetic-parent on synthetic-main,
// each with one commit of its own, and leaves synthetic-main checked out.
func stackedPair(t *testing.T) (testutil.GitRepo, graph.Service) {
	t.Helper()
	repo := testutil.NewGitRepo(t, "synthetic-main")
	repo.Commit("synthetic root", "root.txt", "root")
	repo.Run("switch", "-qc", "synthetic-parent")
	repo.Commit("synthetic parent work", "parent.txt", "parent")
	repo.Run("switch", "-qc", "synthetic-child")
	repo.Commit("synthetic child work", "child.txt", "child")
	t.Chdir(repo.Dir)
	client := git.Client{Runner: subprocess.ExecRunner{}}
	service := graph.Service{Git: client, Store: graph.FileStore{Git: client}, Refs: client}
	for _, edge := range [][2]string{{"synthetic-parent", "synthetic-main"}, {"synthetic-child", "synthetic-parent"}} {
		plan, err := service.PlanTrack(context.Background(), graph.Selection{Branch: edge[0]}, edge[1])
		if err != nil {
			t.Fatal(err)
		}
		if err := service.ApplyTrack(context.Background(), plan); err != nil {
			t.Fatal(err)
		}
	}
	repo.Run("switch", "-q", "synthetic-main")
	return repo, service
}

func statesOf(t *testing.T, service graph.Service) map[string]graph.NodeState {
	t.Helper()
	discovery, err := service.Discover(context.Background(), graph.Selection{Branch: "synthetic-main", Scope: graph.ScopeStack})
	if err != nil {
		t.Fatal(err)
	}
	return discovery.States
}

// A child still sitting on its parent's tip was never asked whether it had
// landed, because one on a trunk cannot have without having no work of its
// own. One on a feature branch can: here parent and child are squash-merged
// together, so the child's work is in the trunk while it sits exactly where it
// was recorded. It read as an ordinary branch under a landed one, and prune
// refused to forget the parent for stranding it.
func TestAChildSquashedWithItsParentReadsAsLanded(t *testing.T) {
	repo, service := stackedPair(t)
	repo.Run("merge", "-q", "--squash", "synthetic-child")
	repo.Run("commit", "-qm", "synthetic squash of parent and child")

	states := statesOf(t, service)
	if states["synthetic-parent"] != graph.StateLanded || states["synthetic-child"] != graph.StateLanded {
		t.Errorf("states = %v, want both landed", states)
	}
}

// Only the parent landed, so the child still has work of its own to offer and
// must not read as landed because its parent did.
func TestAChildOfALandedParentWithWorkLeftIsNotLanded(t *testing.T) {
	repo, service := stackedPair(t)
	repo.Run("merge", "-q", "--squash", "synthetic-parent")
	repo.Run("commit", "-qm", "synthetic squash of the parent alone")

	states := statesOf(t, service)
	if states["synthetic-parent"] != graph.StateLanded {
		t.Fatalf("parent = %s, want landed", states["synthetic-parent"])
	}
	if states["synthetic-child"] != graph.StateAligned {
		t.Errorf("child = %s, want aligned: its own work is not in the trunk", states["synthetic-child"])
	}
}

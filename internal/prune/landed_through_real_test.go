package prune

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
)

// stackedRepository records each child on its parent, every branch with one
// commit of its own, built in the order given, and leaves synthetic-main
// checked out.
func stackedRepository(t *testing.T, edges ...[2]string) (testutil.GitRepo, Service) {
	t.Helper()
	repo := testutil.NewGitRepo(t, "synthetic-main")
	repo.Commit("synthetic root", "root.txt", "root")
	t.Chdir(repo.Dir)
	client := git.Client{Runner: subprocess.ExecRunner{}}
	graphs := graph.Service{Git: client, Store: graph.FileStore{Git: client}, Refs: client}
	for _, edge := range edges {
		repo.Run("switch", "-q", edge[1])
		repo.Run("switch", "-qc", edge[0])
		repo.Commit(edge[0]+" work", edge[0]+".txt", edge[0])
		plan, err := graphs.PlanTrack(context.Background(), graph.Selection{Branch: edge[0]}, edge[1])
		if err != nil {
			t.Fatal(err)
		}
		if err := graphs.ApplyTrack(context.Background(), plan); err != nil {
			t.Fatal(err)
		}
	}
	repo.Run("switch", "-q", "synthetic-main")
	return repo, Service{Git: client, Graph: graphs, Cleaner: client}
}

func squashInto(repo testutil.GitRepo, branches ...string) {
	for _, branch := range branches {
		repo.Run("merge", "-q", "--squash", branch)
		repo.Run("commit", "-qm", "synthetic squash of "+branch)
	}
}

var wholeStack = graph.Selection{Branch: "synthetic-main", Scope: graph.ScopeStack}

// A parent and child squash-merged together land the child's work in the
// trunk and not in the parent branch. prune asked only whether the child had
// anything left for its parent, kept it, and then refused to forget the parent
// for stranding it — about a stack that had landed whole.
func TestAChildSquashedWithItsParentIsForgottenAndDeletedWithIt(t *testing.T) {
	repo, service := stackedRepository(t, [2]string{"synthetic-parent", "synthetic-main"}, [2]string{"synthetic-child", "synthetic-parent"})
	squashInto(repo, "synthetic-child")

	plan, err := service.PlanWithOptions(context.Background(), wholeStack, Options{DeleteBranches: true})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Blocked() != "" || !slices.Equal(plan.Landed, []string{"synthetic-parent", "synthetic-child"}) {
		t.Fatalf("Landed = %v, Blocked = %q; want both forgotten", plan.Landed, plan.Blocked())
	}
	if err := service.Apply(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if out := repo.Run("branch", "--list", "synthetic-parent", "synthetic-child"); strings.TrimSpace(out) != "" {
		t.Errorf("branches survived: %s", out)
	}
}

// Two children under one landed parent are each asked, not just the first.
func TestEveryChildOfALandedParentIsAsked(t *testing.T) {
	repo, service := stackedRepository(t,
		[2]string{"synthetic-parent", "synthetic-main"},
		[2]string{"synthetic-left", "synthetic-parent"},
		[2]string{"synthetic-right", "synthetic-parent"})
	squashInto(repo, "synthetic-left", "synthetic-right")

	plan, err := service.PlanWithOptions(context.Background(), wholeStack, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Blocked() != "" || len(plan.Landed) != 3 {
		t.Fatalf("Landed = %v, Blocked = %q; want all three forgotten", plan.Landed, plan.Blocked())
	}
}

// Only the parent landed, so the child still has work nowhere else and
// forgetting the parent must still refuse rather than strand it.
func TestAChildWithWorkLeftStillBlocksForgettingItsParent(t *testing.T) {
	repo, service := stackedRepository(t, [2]string{"synthetic-parent", "synthetic-main"}, [2]string{"synthetic-child", "synthetic-parent"})
	squashInto(repo, "synthetic-parent")

	plan, err := service.PlanWithOptions(context.Background(), wholeStack, Options{DeleteBranches: true})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(plan.Landed, "synthetic-child") || plan.Blocked() == "" {
		t.Fatalf("Landed = %v, Blocked = %q; want the child kept and the parent refused", plan.Landed, plan.Blocked())
	}
}

// A branch found landed through a forgotten parent lets the one above it be
// asked in turn, past both.
func TestAChainSquashedTogetherIsForgottenWhole(t *testing.T) {
	repo, service := stackedRepository(t,
		[2]string{"synthetic-parent", "synthetic-main"},
		[2]string{"synthetic-child", "synthetic-parent"},
		[2]string{"synthetic-grandchild", "synthetic-child"})
	squashInto(repo, "synthetic-grandchild")

	plan, err := service.PlanWithOptions(context.Background(), wholeStack, Options{DeleteBranches: true})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Blocked() != "" || !slices.Equal(plan.Landed, []string{"synthetic-parent", "synthetic-child", "synthetic-grandchild"}) {
		t.Fatalf("Landed = %v, Blocked = %q; want the whole chain forgotten", plan.Landed, plan.Blocked())
	}
}

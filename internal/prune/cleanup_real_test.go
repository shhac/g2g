package prune

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
)

func cleanupRepository(t *testing.T) (testutil.GitRepo, Service) {
	t.Helper()
	repo := testutil.NewGitRepo(t, "synthetic-main")
	repo.Commit("synthetic root", "root.txt", "root")
	repo.Run("switch", "-qc", "synthetic-work")
	repo.Commit("synthetic one", "one.txt", "one")
	repo.Commit("synthetic two", "two.txt", "two")
	t.Chdir(repo.Dir)
	client := git.Client{Runner: subprocess.ExecRunner{}}
	graphs := graph.Service{Git: client, Store: graph.FileStore{Git: client}, Refs: client}
	plan, err := graphs.PlanTrack(context.Background(), graph.Selection{Branch: "synthetic-work"}, "synthetic-main")
	if err != nil {
		t.Fatal(err)
	}
	if err := graphs.ApplyTrack(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	repo.Run("switch", "-q", "synthetic-main")
	repo.Run("merge", "-q", "--squash", "synthetic-work")
	repo.Run("commit", "-qm", "synthetic squash")
	return repo, Service{Git: client, Graph: graphs, Cleaner: client}
}

func TestCleanupDeletesASquashMergedBranchAndKeepsUnlandedWork(t *testing.T) {
	repo, service := cleanupRepository(t)
	repo.Run("switch", "-qc", "synthetic-unlanded")
	repo.Commit("synthetic new work", "new.txt", "new")
	track, err := service.Graph.PlanTrack(context.Background(), graph.Selection{Branch: "synthetic-unlanded"}, "synthetic-main")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Graph.ApplyTrack(context.Background(), track); err != nil {
		t.Fatal(err)
	}
	repo.Run("switch", "-q", "synthetic-main")
	selection := graph.Selection{Branch: "synthetic-main", Scope: graph.ScopeAll}
	plan, err := service.PlanWithOptions(context.Background(), selection, Options{DeleteBranches: true})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(plan.Deleted(), []string{"synthetic-work"}) {
		t.Fatalf("deletions = %v", plan.Deleted())
	}
	if !strings.Contains(repo.Run("branch", "--list", "synthetic-work"), "synthetic-work") {
		t.Fatal("preview deleted a branch")
	}
	validated, err := service.Revalidate(context.Background(), selection, plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Apply(context.Background(), validated); err != nil {
		t.Fatal(err)
	}
	if out := repo.Run("branch", "--list", "synthetic-work"); strings.TrimSpace(out) != "" {
		t.Fatalf("branch survived: %s", out)
	}
	if out := repo.Run("branch", "--list", "synthetic-unlanded"); !strings.Contains(out, "synthetic-unlanded") {
		t.Fatal("unlanded work deleted")
	}
	if out := repo.Run("status", "--porcelain"); out != "" {
		t.Fatalf("cleanup dirtied the tree: %s", out)
	}
	stored, err := service.Graph.Store.Load(context.Background())
	if err != nil || stored.Tracked("synthetic-work") || !stored.Tracked("synthetic-unlanded") {
		t.Fatalf("graph after cleanup: %+v, %v", stored, err)
	}
}

func TestCleanupRefusesAChangedTipEvenAfterRevalidation(t *testing.T) {
	repo, service := cleanupRepository(t)
	plan, err := service.PlanWithOptions(context.Background(), graph.Selection{Branch: "synthetic-main", Scope: graph.ScopeAll}, Options{DeleteBranches: true})
	if err != nil {
		t.Fatal(err)
	}
	repo.Run("switch", "-q", "synthetic-work")
	repo.Commit("synthetic work after merge", "later.txt", "later")
	repo.Run("switch", "-q", "synthetic-main")
	if err := service.Apply(context.Background(), plan); err == nil {
		t.Fatal("deleted a tip that was never assessed")
	}
	if !strings.Contains(repo.Run("branch", "--list", "synthetic-work"), "synthetic-work") {
		t.Fatal("changed branch deleted")
	}
	if out := repo.Run("status", "--porcelain"); out != "" {
		t.Fatalf("tree dirty: %s", out)
	}
}

func TestCleanupKeepsWorkAddedAfterAMerge(t *testing.T) {
	repo, service := cleanupRepository(t)
	repo.Run("switch", "-q", "synthetic-work")
	repo.Commit("synthetic followup", "followup.txt", "followup")
	repo.Run("switch", "-q", "synthetic-main")
	plan, err := service.PlanWithOptions(context.Background(), graph.Selection{Branch: "synthetic-main", Scope: graph.ScopeAll}, Options{DeleteBranches: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Delete) != 0 || !plan.Nothing() {
		t.Fatalf("followup selected for deletion: %+v", plan)
	}
}

func TestCleanupRefusesABaseThatChangedAfterAssessment(t *testing.T) {
	repo, service := cleanupRepository(t)
	plan, err := service.PlanWithOptions(context.Background(), graph.Selection{Branch: "synthetic-main", Scope: graph.ScopeAll}, Options{DeleteBranches: true})
	if err != nil {
		t.Fatal(err)
	}
	repo.Run("reset", "-q", "--hard", "HEAD~1")
	if err := service.Apply(context.Background(), plan); err == nil {
		t.Fatal("deleted work after the assessed base was rewound")
	}
	if !strings.Contains(repo.Run("branch", "--list", "synthetic-work"), "synthetic-work") {
		t.Fatal("branch deleted")
	}
}

func TestCleanupRefusesEveryCheckedOutBranch(t *testing.T) {
	for _, elsewhere := range []bool{false, true} {
		t.Run(map[bool]string{false: "current", true: "other worktree"}[elsewhere], func(t *testing.T) {
			repo, service := cleanupRepository(t)
			if elsewhere {
				repo.Run("worktree", "add", "-q", t.TempDir(), "synthetic-work")
			} else {
				repo.Run("switch", "-q", "synthetic-work")
			}
			plan, err := service.PlanWithOptions(context.Background(), graph.Selection{Branch: "synthetic-main", Scope: graph.ScopeAll}, Options{DeleteBranches: true})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(plan.Blocked, "checked out") {
				t.Fatalf("refusal = %q", plan.Blocked)
			}
			if err := service.Apply(context.Background(), plan); err == nil {
				t.Fatal("applied a refused cleanup")
			}
		})
	}
}

type failingCleanupStore struct{ graph.Store }

func (f failingCleanupStore) Save(context.Context, graph.Graph) error {
	return errors.New("synthetic store failure")
}

func TestCleanupReportsDeletionBeforeAFailedGraphWriteAsPartWay(t *testing.T) {
	_, service := cleanupRepository(t)
	plan, err := service.PlanWithOptions(context.Background(), graph.Selection{Branch: "synthetic-main", Scope: graph.ScopeAll}, Options{DeleteBranches: true})
	if err != nil {
		t.Fatal(err)
	}
	service.Graph.Store = failingCleanupStore{service.Graph.Store}
	var stopped *Stopped
	if err := service.Apply(context.Background(), plan); !errors.As(err, &stopped) || !slices.Equal(stopped.Deleted, []string{"synthetic-work"}) {
		t.Fatalf("Apply = %v", err)
	}
}

func TestCleanupKeepsInheritedWorkWhenADeletedParentNeverLanded(t *testing.T) {
	for _, parentLanded := range []bool{false, true} {
		t.Run(map[bool]string{false: "unlanded parent", true: "landed parent"}[parentLanded], func(t *testing.T) {
			repo, service := cleanupRepository(t)
			if !parentLanded {
				// Remove the squash from the base, leaving the parent's work
				// present only on its own ref and the new child's ref.
				repo.Run("reset", "-q", "--hard", "HEAD~1")
			}
			repo.Run("switch", "-qc", "synthetic-child", "synthetic-work")
			ctx := context.Background()
			track, err := service.Graph.PlanTrack(ctx, graph.Selection{Branch: "synthetic-child"}, "synthetic-work")
			if err != nil {
				t.Fatal(err)
			}
			if err := service.Graph.ApplyTrack(ctx, track); err != nil {
				t.Fatal(err)
			}
			repo.Run("switch", "-q", "synthetic-main")
			repo.Run("branch", "-qD", "synthetic-work")
			selection := graph.Selection{Branch: "synthetic-main", Scope: graph.ScopeAll}
			plan, err := service.PlanWithOptions(ctx, selection, Options{DeleteBranches: true, ForgetMissing: true})
			if err != nil {
				t.Fatal(err)
			}
			if slices.Contains(plan.Deleted(), "synthetic-child") != parentLanded {
				t.Fatalf("inherited work assessment: %+v", plan)
			}
			if err := service.Apply(ctx, plan); err != nil {
				t.Fatal(err)
			}
			if !parentLanded {
				// The child keeps the inherited work while cleanup records
				// its evidenced ancestry on the surviving base.
				repo.Run("cat-file", "-e", "synthetic-child:one.txt")
				repo.Run("cat-file", "-e", "synthetic-child:two.txt")
			}
			if got := repo.Run("branch", "--list", "synthetic-child"); strings.Contains(got, "synthetic-child") == parentLanded {
				t.Fatalf("wrong surviving refs: %s", got)
			}
			if got := repo.Run("status", "--porcelain"); got != "" {
				t.Fatalf("tree dirty: %s", got)
			}
		})
	}
}

func TestCleanupKeepsInheritedWorkWhenRecordedRangesDrift(t *testing.T) {
	for _, resetChild := range []bool{false, true} {
		t.Run(map[bool]string{false: "parent rewound", true: "child reset below fork"}[resetChild], func(t *testing.T) {
			repo := testutil.NewGitRepo(t, "synthetic-main")
			repo.Commit("synthetic root", "root.txt", "root")
			repo.Commit("synthetic inherited work", "inherited.txt", "inherited")
			inherited := strings.TrimSpace(repo.Run("rev-parse", "HEAD"))
			repo.Commit("synthetic later work", "later.txt", "later")
			repo.Run("switch", "-qc", "synthetic-work")
			t.Chdir(repo.Dir)
			client := git.Client{Runner: subprocess.ExecRunner{}}
			graphs := graph.Service{Git: client, Store: graph.FileStore{Git: client}, Refs: client}
			ctx := context.Background()
			track, err := graphs.PlanTrack(ctx, graph.Selection{Branch: "synthetic-work"}, "synthetic-main")
			if err != nil {
				t.Fatal(err)
			}
			if err := graphs.ApplyTrack(ctx, track); err != nil {
				t.Fatal(err)
			}
			// The base no longer has the inherited work. Whether the child
			// stayed at its fork or moved below it, its own range is empty.
			repo.Run("switch", "-q", "synthetic-main")
			repo.Run("reset", "-q", "--hard", "HEAD~2")
			if resetChild {
				repo.Run("switch", "-q", "synthetic-work")
				repo.Run("reset", "-q", "--hard", inherited)
				repo.Run("switch", "-q", "synthetic-main")
			}
			service := Service{Git: client, Graph: graphs, Cleaner: client}
			plan, err := service.PlanWithOptions(ctx, graph.Selection{Branch: "synthetic-main", Scope: graph.ScopeAll}, Options{DeleteBranches: true})
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Delete) != 0 || !plan.Nothing() {
				t.Fatalf("stale range excluded unlanded work: %+v", plan)
			}
			if err := service.Apply(ctx, plan); err != nil {
				t.Fatal(err)
			}
			repo.Run("cat-file", "-e", "synthetic-work:inherited.txt")
			if got := repo.Run("status", "--porcelain"); got != "" {
				t.Fatalf("tree dirty: %s", got)
			}
		})
	}
}

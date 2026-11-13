package cli

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/restack"
	"github.com/shhac/g2g/internal/stack"
	"github.com/shhac/g2g/internal/submit"
	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
	"github.com/spf13/cobra"
)

func TestPlanningPassesHaveIndependentBudgets(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	calls := 0
	flow := applyFlow[interruptedPlan]{
		plan: func(ctx context.Context) (interruptedPlan, error) {
			calls++
			if calls == 1 {
				time.Sleep(200 * time.Millisecond)
			} else if got := remaining(t, ctx); got < 900*time.Millisecond {
				t.Fatalf("revalidation had only %s", got)
			}
			return interruptedPlan{}, nil
		},
		render:  func(io.Writer, interruptedPlan, Presentation) error { return nil },
		execute: func(context.Context, interruptedPlan) error { return nil },
	}
	if err := flow.run(cmd, context.Background(), budgets{override: time.Second}, Presentation{}, true); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("planning passes %d", calls)
	}
}

func TestEachPlanningPassCancelsBeforeMutation(t *testing.T) {
	for _, slow := range []int{1, 2} {
		cmd := &cobra.Command{}
		cmd.SetOut(io.Discard)
		calls := 0
		mutated := false
		flow := applyFlow[interruptedPlan]{
			plan: func(ctx context.Context) (interruptedPlan, error) {
				calls++
				if calls == slow {
					<-ctx.Done()
					return interruptedPlan{}, ctx.Err()
				}
				return interruptedPlan{}, nil
			},
			execute: func(context.Context, interruptedPlan) error { mutated = true; return nil },
		}
		if err := flow.run(cmd, context.Background(), budgets{override: time.Millisecond}, Presentation{}, true); err == nil || !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("pass %d: %v", slow, err)
		}
		if mutated {
			t.Fatal("mutation after planning timeout")
		}
	}
}

func TestRevalidationPrefersSpecificBlockerAndRepeatsGuard(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	for _, guarded := range []bool{false, true} {
		calls, guards := 0, 0
		flow := applyFlow[interruptedPlan]{
			guard: guardFunc(func() error {
				guards++
				if guarded && guards == 2 {
					return errors.New("restack belongs to synthetic owner's worktree")
				}
				return nil
			}),
			plan: func(context.Context) (interruptedPlan, error) {
				calls++
				return interruptedPlan{name: strings.Repeat("x", calls)}, nil
			},
			blocked: func(p interruptedPlan) string {
				if p.name == "xx" {
					return "synthetic required check failed"
				}
				return ""
			},
			execute: func(context.Context, interruptedPlan) error { t.Fatal("blocked apply executed"); return nil },
		}
		err := flow.run(cmd, context.Background(), budgets{}, Presentation{}, true)
		want := "synthetic required check failed"
		if guarded {
			want = "synthetic owner's worktree"
		}
		if err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "plan changed") {
			t.Fatalf("got %v want %s", err, want)
		}
		if guards != 2 {
			t.Fatalf("guard called %d times", guards)
		}
	}
}

func TestMinimalSpecCreationRequirementsChangingRefusesBeforePublication(t *testing.T) {
	for _, changedState := range []string{"CLOSED", "MERGED", ""} {
		cmd := &cobra.Command{}
		cmd.SetOut(io.Discard)
		initial := submit.Plan{Snapshot: stack.Snapshot{Base: "synthetic-main", Branches: []string{"synthetic-existing"}}, Existing: []githubstack.PullRequest{{Head: "synthetic-existing", State: "OPEN", Number: 1}}}
		current := initial
		if changedState == "" {
			current.Existing = nil
		} else {
			current.Existing = []githubstack.PullRequest{{Head: "synthetic-existing", State: changedState, Number: 1}}
		}
		calls := 0
		flow := applyFlow[submit.Plan]{
			plan: func(context.Context) (submit.Plan, error) {
				calls++
				if calls == 1 {
					return initial, nil
				}
				return current, nil
			},
			revalidation: revalidation{"submit", "submit plan"},
			execute: func(context.Context, submit.Plan) error {
				t.Fatal("changed creation requirements published")
				return nil
			},
		}
		err := flow.run(cmd, context.Background(), budgets{}, Presentation{}, true)
		if err == nil || !strings.Contains(err.Error(), "changed") {
			t.Fatalf("lifecycle %q error %v", changedState, err)
		}
	}
}

func TestAnotherWorktreeStartingRestackBetweenPlansNamesItsCheckout(t *testing.T) {
	repo := testutil.NewGitRepo(t, "synthetic-main")
	repo.Commit("synthetic root", "root.txt", "root")
	repo.Run("branch", "synthetic-owner")
	owner := repo.Worktree("synthetic-owner")
	privateDir := strings.TrimSpace(owner.Run("rev-parse", "--absolute-git-dir"))
	t.Chdir(repo.Dir)
	git := localgit.Client{Runner: subprocess.ExecRunner{}}
	service := restack.Service{Journal: restack.FileJournal{Git: git}}
	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	calls := 0
	flow := applyFlow[interruptedPlan]{
		guard: restackGuard(service),
		plan: func(ctx context.Context) (interruptedPlan, error) {
			calls++
			err := service.Journal.Save(ctx, restack.Record{Worktree: privateDir, Branch: "synthetic-owner"})
			return interruptedPlan{}, err
		},
		execute: func(context.Context, interruptedPlan) error {
			t.Fatal("mutation while other worktree owns restack")
			return nil
		},
	}
	err := flow.run(cmd, context.Background(), budgets{}, Presentation{}, true)
	if err == nil || !strings.Contains(err.Error(), owner.Dir) || !strings.Contains(err.Error(), "synthetic-owner") {
		t.Fatalf("owner advice %v", err)
	}
	if calls != 1 {
		t.Fatalf("read half-restacked graph %d times", calls)
	}
}

// guardFunc is an idle guard that answers both questions the same way.
type guardFunc func() error

func (g guardFunc) RequireIdle(context.Context) error              { return g() }
func (g guardFunc) RequireIdleFor(context.Context, []string) error { return g() }

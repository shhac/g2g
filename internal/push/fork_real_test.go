package push

import (
	"context"
	"testing"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/stack"
	"github.com/shhac/g2g/internal/subprocess"
)

// A fork is one atomic push, and leaves nothing behind on either arm.
func TestAForkedSelectionIsPublishedInOneAtomicPush(t *testing.T) {
	repo := publishedStack(t)
	repo.Run("checkout", "-q", "-b", "synthetic-side", "synthetic-lower")
	repo.Commit("synthetic side", "side.txt", "side")
	repo.Run("checkout", "-q", "synthetic-top")
	repo.Commit("synthetic more top", "top.txt", "top, more")
	t.Chdir(repo.Dir)

	service := Service{Git: localgit.Client{Runner: subprocess.ExecRunner{}}, Selector: forkedPath{}}
	plan, err := service.Plan(context.Background(), stack.Selection{}, "origin", localgit.SetUpstream)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if err := service.Execute(context.Background(), plan); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	for _, branch := range []string{"synthetic-lower", "synthetic-top", "synthetic-side"} {
		if got := repo.Run("rev-parse", "origin/"+branch); got != repo.Revision(branch) {
			t.Errorf("origin/%s = %s, want %s", branch, got, repo.Revision(branch))
		}
	}
}

// forkedPath is main ← lower ← {top, side}, selected from lower.
type forkedPath struct{}

func (forkedPath) Select(context.Context, stack.Selection, string) (stack.Snapshot, error) {
	return stack.Snapshot{
		Target: "synthetic-lower", Base: "synthetic-main",
		Branches: []string{"synthetic-lower", "synthetic-top", "synthetic-side"},
		Parents:  map[string]string{"synthetic-lower": "synthetic-main", "synthetic-top": "synthetic-lower", "synthetic-side": "synthetic-lower"},
	}, nil
}

package push

import (
	"context"
	"strings"
	"testing"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/stack"
	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
)

func TestDifferenceWithUnfetchedPublishedParentKeepsRefusal(t *testing.T) {
	repo := publishedStack(t)
	colleague := testutil.NewGitRepo(t, "synthetic-main")
	remote := repo.Run("remote", "get-url", "origin")
	colleague.Run("fetch", "-q", remote, "synthetic-top:synthetic-top", "synthetic-lower:synthetic-lower")
	colleague.Run("switch", "-q", "synthetic-top")
	colleague.Commit("synthetic reviewer child", "review.txt", "synthetic review")
	colleague.Run("push", "-q", remote, "synthetic-top")
	repo.Run("fetch", "-q", "origin", "synthetic-top")
	colleague.Run("switch", "-q", "synthetic-lower")
	colleague.Commit("synthetic unfetched parent", "parent-review.txt", "synthetic parent review")
	colleague.Run("push", "-q", remote, "synthetic-lower")
	t.Chdir(repo.Dir)
	service := Service{Git: localgit.Client{Runner: subprocess.ExecRunner{}}, Selector: linePath{}}
	plan, err := service.Plan(context.Background(), stack.Selection{}, "origin", localgit.SetUpstream)
	if err != nil {
		t.Fatalf("valid refusal replaced by Git error: %v", err)
	}
	if plan.Publishing["synthetic-lower"].Standing != Unknown || plan.Publishing["synthetic-top"].Standing != Behind {
		t.Fatalf("classifications = %+v", plan.Publishing)
	}
	if !strings.Contains(plan.Blocked(), "published version differs") || len(plan.Repair.Ways) < 3 {
		t.Fatalf("refusal or ways lost: %+v", plan.Repair)
	}
	if strings.Contains(plan.Blocked(), "synthetic reviewer child") {
		t.Fatal("unavailable parent evidence named child commits")
	}
}

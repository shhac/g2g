package push

import (
	"context"
	"testing"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/stack"
	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
)

// Whether a branch tracks what was published is Git's own config, written by
// the push itself, so only a real push can say it was written.
func TestAPushSetsEveryPublishedBranchsUpstream(t *testing.T) {
	repo := publishedStack(t)
	repo.Commit("synthetic more top", "top.txt", "top, more")

	publish(t, repo, localgit.SetUpstream)

	// synthetic-lower was already on the remote exactly, and is set as well:
	// the push names it, and git records every branch a push named.
	for _, branch := range []string{"synthetic-lower", "synthetic-top"} {
		if got, want := repo.Run("rev-parse", "--abbrev-ref", branch+"@{upstream}"), "origin/"+branch; got != want {
			t.Errorf("%s tracks %q, want %q", branch, got, want)
		}
	}
}

func TestNoSetUpstreamLeavesWhatEachBranchTrackedAlone(t *testing.T) {
	repo := publishedStack(t)
	repo.Commit("synthetic more top", "top.txt", "top, more")
	repo.Run("branch", "-q", "--set-upstream-to=origin/synthetic-main", "synthetic-lower")

	publish(t, repo, localgit.LeaveUpstream)

	if got := repo.Run("rev-parse", "--abbrev-ref", "synthetic-lower@{upstream}"); got != "origin/synthetic-main" {
		t.Errorf("synthetic-lower tracks %q, want the upstream it already had", got)
	}
	if err := repo.Try("rev-parse", "--abbrev-ref", "synthetic-top@{upstream}"); err == nil {
		t.Error("synthetic-top has an upstream, want none")
	}
	if got := repo.Run("rev-parse", "origin/synthetic-top"); got != repo.Revision("synthetic-top") {
		t.Error("synthetic-top was not published")
	}
}

func publish(t *testing.T, repo testutil.GitRepo, upstream localgit.Upstream) {
	t.Helper()
	t.Chdir(repo.Dir)
	service := Service{Git: localgit.Client{Runner: subprocess.ExecRunner{}}, Selector: linePath{}}
	plan, err := service.Plan(context.Background(), stack.Selection{}, "origin", upstream)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if err := service.Execute(context.Background(), plan); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
}

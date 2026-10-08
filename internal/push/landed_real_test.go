package push

import (
	"context"
	"testing"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/subprocess"
)

// A branch squash-merged and deleted on the remote reads as landed, and the
// push that publishes the branch above it must not put it back. Only a real
// remote can say which refs a push created.
func TestAPushDoesNotRecreateALandedBranchOnTheRemote(t *testing.T) {
	repo := publishedStack(t)
	repo.Run("checkout", "-q", "synthetic-main")
	repo.Run("merge", "-q", "--squash", "synthetic-lower")
	repo.Run("commit", "-q", "-m", "synthetic squash of lower")
	repo.Run("push", "-q", "origin", "synthetic-main", ":synthetic-lower")
	repo.Run("checkout", "-q", "synthetic-top")
	repo.Commit("synthetic more top", "top.txt", "top, more")

	plan := planPush(t, repo)
	if got := plan.Publishing["synthetic-lower"].Standing; got != Landed {
		t.Fatalf("synthetic-lower stands %v, want Landed", got)
	}
	service := Service{Git: localgit.Client{Runner: subprocess.ExecRunner{}}, Selector: linePath{}}
	if err := service.Execute(context.Background(), plan); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if err := repo.Try("ls-remote", "--exit-code", "origin", "refs/heads/synthetic-lower"); err == nil {
		t.Error("synthetic-lower is back on the remote after it landed")
	}
	if got := repo.Run("ls-remote", "origin", "refs/heads/synthetic-top"); got == "" || got[:40] != repo.Revision("synthetic-top") {
		t.Errorf("synthetic-top on the remote = %q, want it published", got)
	}
}

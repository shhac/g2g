package link

import (
	"context"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/stack"
	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
)

// A fake proves nothing here. The question is what Git itself considers
// equivalent after a replay, and a fake answers whatever it is asked — which
// is how a count that grew by the size of the trunk survived a suite that had
// tests for it.
//
// So this builds a throwaway repository, no remote and every name invented,
// and puts a branch in the state a restacked stack is actually in: the branch
// replayed onto a trunk that has moved on, its pull request still on the
// commit it was pushed with.
func TestTheCountIsThisBranchesOwnWorkAndNotTheTrunksCommits(t *testing.T) {
	repo := testutil.NewGitRepo(t, "synthetic-main")
	repo.Commit("synthetic root", "root.txt", "root")

	repo.Run("checkout", "-q", "-b", "synthetic-top")
	repo.Commit("synthetic own one", "own-one.txt", "one")
	repo.Commit("synthetic own two", "own-two.txt", "two")
	// Where the pull request was pushed from, before anything moved.
	pushed := strings.TrimSpace(repo.Run("rev-parse", "HEAD"))

	// The trunk moves on by more than the branch will ever hold, which is the
	// shape that made the old count read as hundreds of unpushed commits.
	repo.Run("checkout", "-q", "synthetic-main")
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		repo.Commit("synthetic trunk "+name, "trunk-"+name+".txt", name)
	}
	repo.Run("checkout", "-q", "synthetic-top")
	repo.Run("rebase", "-q", "synthetic-main")

	t.Chdir(repo.Dir)
	service := Service{Tips: git.Client{Runner: subprocess.ExecRunner{}}}
	plan := Plan{Discovery: stack.Discovery{Snapshot: stack.Snapshot{
		Target:   "synthetic-top",
		Base:     "synthetic-main",
		Branches: []string{"synthetic-top"},
	}, PullRequests: []githubstack.PullRequest{
		{Number: 7, Head: "synthetic-top", HeadOID: pushed, Base: "synthetic-main", State: "OPEN"},
	}}}

	currency, err := service.currency(context.Background(), plan)
	if err != nil {
		t.Fatalf("currency() error = %v", err)
	}

	// Nothing is missing from the pull request: it holds both of this branch's
	// commits, as the commits it was pushed with. The five the trunk gained
	// are not this branch's work and must not be counted as unpushed.
	if got, want := currency["synthetic-top"], (Currency{Rewritten: true}); got != want {
		t.Errorf("currency = %+v, want %+v", got, want)
	}
}

// The same repository with one genuinely unpushed commit: the count is that
// commit, and still not the trunk's.
func TestOnlyTheBranchesOwnUnpushedWorkIsCounted(t *testing.T) {
	repo := testutil.NewGitRepo(t, "synthetic-main")
	repo.Commit("synthetic root", "root.txt", "root")

	repo.Run("checkout", "-q", "-b", "synthetic-top")
	repo.Commit("synthetic own one", "own-one.txt", "one")
	pushed := strings.TrimSpace(repo.Run("rev-parse", "HEAD"))

	repo.Run("checkout", "-q", "synthetic-main")
	for _, name := range []string{"a", "b", "c"} {
		repo.Commit("synthetic trunk "+name, "trunk-"+name+".txt", name)
	}
	repo.Run("checkout", "-q", "synthetic-top")
	repo.Run("rebase", "-q", "synthetic-main")
	repo.Commit("synthetic own two", "own-two.txt", "two")

	t.Chdir(repo.Dir)
	service := Service{Tips: git.Client{Runner: subprocess.ExecRunner{}}}
	plan := Plan{Discovery: stack.Discovery{Snapshot: stack.Snapshot{
		Target:   "synthetic-top",
		Base:     "synthetic-main",
		Branches: []string{"synthetic-top"},
	}, PullRequests: []githubstack.PullRequest{
		{Number: 7, Head: "synthetic-top", HeadOID: pushed, Base: "synthetic-main", State: "OPEN"},
	}}}

	currency, err := service.currency(context.Background(), plan)
	if err != nil {
		t.Fatalf("currency() error = %v", err)
	}

	if got, want := currency["synthetic-top"], (Currency{Unpushed: 1}); got != want {
		t.Errorf("currency = %+v, want %+v", got, want)
	}
}

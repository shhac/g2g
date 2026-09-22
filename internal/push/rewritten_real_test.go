package push

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/stack"
	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
)

// linePath answers with a fixed two-branch line, so these tests are about
// what the remote holds rather than how the stack was selected.
type linePath struct{}

func (linePath) Select(context.Context, stack.Selection, string) (stack.Snapshot, error) {
	return stack.Snapshot{
		Target: "synthetic-top", Base: "synthetic-main",
		Branches: []string{"synthetic-lower", "synthetic-top"},
		Parents:  map[string]string{"synthetic-lower": "synthetic-main", "synthetic-top": "synthetic-lower"},
	}, nil
}

// publishedStack is main ← lower ← top, pushed to a bare remote that stands in
// for origin. Nothing leaves the machine.
func publishedStack(t *testing.T) testutil.GitRepo {
	t.Helper()
	remote := t.TempDir()
	if output, err := exec.Command("git", "init", "-q", "--bare", remote).CombinedOutput(); err != nil {
		t.Fatalf("init remote: %v\n%s", err, output)
	}
	repo := testutil.NewGitRepo(t, "synthetic-main")
	repo.Run("remote", "add", "origin", remote)
	repo.Commit("synthetic root", "root.txt", "root")
	repo.Run("checkout", "-q", "-b", "synthetic-lower")
	repo.Commit("synthetic lower", "lower.txt", "lower")
	repo.Run("checkout", "-q", "-b", "synthetic-top")
	repo.Commit("synthetic top", "top.txt", "top")
	repo.Run("push", "-q", "origin", "synthetic-main", "synthetic-lower", "synthetic-top")
	return repo
}

func planPush(t *testing.T, repo testutil.GitRepo) Plan {
	t.Helper()
	t.Chdir(repo.Dir)
	service := Service{Git: localgit.Client{Runner: subprocess.ExecRunner{}}, Selector: linePath{}}
	plan, err := service.Plan(context.Background(), stack.Selection{}, "origin")
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	return plan
}

// The ordinary loop: fix the branch below, restack the one above, publish.
// The remote's version of the upper branch is no longer an ancestor, and
// every commit it holds is here under a new id. Counting by id called that
// somebody else's work, and refused the push the restack exists to enable.
func TestAReplayedBranchIsPublishedRatherThanRefused(t *testing.T) {
	repo := publishedStack(t)
	repo.Run("checkout", "-q", "synthetic-lower")
	repo.Commit("synthetic lower fix", "lower.txt", "lower, fixed")
	repo.Run("rebase", "-q", "--onto", "synthetic-lower", "synthetic-lower@{1}", "synthetic-top")

	plan := planPush(t, repo)
	if plan.Blocked != "" {
		t.Fatalf("a restacked stack is refused: %s", plan.Blocked)
	}
	top := plan.Publishing["synthetic-top"]
	if !top.Rewritten || top.Theirs != 0 || top.Rejected() {
		t.Errorf("synthetic-top = %+v, want rewritten and nothing the remote would lose", top)
	}
}

// A reviewer's commit on the remote is work this checkout does not have, by
// content as much as by id, and publishing over it would drop it.
func TestAColleaguesCommitOnTheRemoteIsStillRefused(t *testing.T) {
	repo := publishedStack(t)
	colleague := testutil.NewGitRepo(t, "synthetic-main")
	colleague.Run("fetch", "-q", repo.Run("remote", "get-url", "origin"), "synthetic-top:synthetic-top")
	colleague.Run("checkout", "-q", "synthetic-top")
	colleague.Commit("synthetic review fix", "review.txt", "review")
	colleague.Run("push", "-q", repo.Run("remote", "get-url", "origin"), "synthetic-top")
	repo.Run("fetch", "-q", "origin")

	repo.Run("checkout", "-q", "synthetic-top")
	repo.Commit("synthetic local work", "local.txt", "local")

	plan := planPush(t, repo)
	if plan.Blocked == "" || !strings.Contains(plan.Blocked, "synthetic-top") {
		t.Fatalf("Blocked = %q, want the remote's new commit protected", plan.Blocked)
	}
	if top := plan.Publishing["synthetic-top"]; top.Theirs != 1 || top.Rewritten {
		t.Errorf("synthetic-top = %+v, want one commit only on the remote", top)
	}
}

package land

import (
	"context"
	"testing"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/landed"
	"github.com/shhac/g2g/internal/push"
	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
)

// A reviewer reverts part of a branch's work after it was squashed into the
// trunk. The revert merges into that trunk without changing it, so the
// whole-branch content check reads it as landed, though it exists on the
// remote alone. What a fake cannot vouch for is what Git makes of that, so
// this asks real Git, through the same assessment the preflight makes.
func TestAPostMergeRevertIsNeverAHeadTheRemoteMayBeDeletedAt(t *testing.T) {
	repo := testutil.NewGitRepo(t, "synthetic-main")
	repo.Commit("synthetic root", "root.txt", "root")
	repo.Run("switch", "-qc", "synthetic-work")
	repo.Commit("synthetic first change", "a.txt", "a")
	repo.Commit("synthetic second change", "b.txt", "b")
	repo.Run("switch", "-q", "synthetic-main")
	repo.Run("merge", "-q", "--squash", "synthetic-work")
	repo.Run("commit", "-qm", "synthetic squash")
	collapsed := repo.Revision("HEAD")
	repo.Run("switch", "-q", "synthetic-work")
	repo.Run("revert", "--no-edit", "HEAD")
	reverted := repo.Revision("HEAD")
	// The replay here collapsed the branch onto the trunk; the remote holds
	// the revert.
	repo.Run("switch", "-q", "synthetic-main")
	repo.Run("branch", "-f", "synthetic-work", collapsed)
	t.Chdir(repo.Dir)
	ctx := context.Background()
	git := localgit.Client{Runner: subprocess.ExecRunner{}}

	// The trap is real: the whole-branch check calls the revert landed.
	if absorbed, err := landed.Into(ctx, git, "synthetic-main", reverted, ""); err != nil || !absorbed {
		t.Fatalf("landed.Into(revert) = %v, %v; the scenario needs it to read as landed", absorbed, err)
	}

	tips := map[string]string{"synthetic-work": reverted}
	publishing, err := push.Compare(ctx, git, "", []string{"synthetic-work"}, tips, func(string) (string, string) {
		return "synthetic-main", "synthetic-main"
	})
	if err != nil {
		t.Fatal(err)
	}
	checked := checkedTips(push.Plan{Publishing: publishing, RemoteTips: tips})
	if tip, ok := checked["synthetic-work"]; ok {
		t.Fatalf("checkedTips = %s (standing %v), want the revert left unchecked", tip, publishing["synthetic-work"].Standing)
	}
	if got := deletableHead(collapsed, checked["synthetic-work"]); got != collapsed {
		t.Errorf("deletableHead = %s, want the head here, %s, which the revert's remote branch does not hold", got, collapsed)
	}
}

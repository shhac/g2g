package landed_test

import (
	"context"
	"testing"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/landed"
	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
)

func TestPublishedParentEvidenceDoesNotExcludeWorkOnlyInLocalParent(t *testing.T) {
	repo := testutil.NewGitRepo(t, "synthetic-main")
	repo.Commit("synthetic root", "root.txt", "synthetic root")
	repo.Run("switch", "-qc", "synthetic-parent")
	repo.Commit("synthetic parent", "parent.txt", "synthetic parent")
	published := repo.Revision("synthetic-parent")
	repo.Run("switch", "-qc", "synthetic-child")
	repo.Commit("synthetic child", "child.txt", "synthetic child")
	repo.Run("switch", "-q", "synthetic-parent")
	repo.Run("merge", "--ff-only", "synthetic-child")
	t.Chdir(repo.Dir)
	probe := localgit.Client{Runner: subprocess.ExecRunner{}}
	if got := landed.AssessPublishedParent(context.Background(), probe, "synthetic-child", published); got != landed.Absent {
		t.Fatalf("published parent = %v, want absent; local parent hid the entire child range", got)
	}
}

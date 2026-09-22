package git

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
)

// These are questions about what Git holds and what it will move, which a PATH
// fake cannot answer: it replies with whatever it was told to.

// publishedRepo is a trunk with a branch of two commits, the first of which a
// synthetic remote-tracking ref also reaches.
func publishedRepo(t *testing.T) (testutil.GitRepo, Client) {
	t.Helper()
	repo := testutil.NewGitRepo(t, "synthetic-main")
	repo.Commit("synthetic root", "root.txt", "root")
	repo.Run("switch", "-qc", "synthetic-work")
	repo.Commit("synthetic pushed", "pushed.txt", "pushed")
	repo.Run("update-ref", "refs/remotes/origin/synthetic-work", "HEAD")
	repo.Commit("synthetic local", "local.txt", "local")
	inRepo(t, repo.Dir)
	return repo, Client{Runner: subprocess.ExecRunner{}}
}

func TestUnpublishedListsOnlyWhatNoRemoteTrackingRefReaches(t *testing.T) {
	repo, client := publishedRepo(t)

	commits, err := client.Unpublished(context.Background(), "synthetic-work", "synthetic-main")
	if err != nil {
		t.Fatalf("Unpublished() error = %v", err)
	}

	want := []Commit{{ID: repo.Revision("synthetic-work"), Subject: "synthetic local"}}
	if !slices.Equal(commits, want) {
		t.Errorf("Unpublished() = %+v, want %+v", commits, want)
	}
}

func TestRemoteTrackingNamesEveryRemoteCarryingTheBranchAndNoPrefixOfIt(t *testing.T) {
	repo, client := publishedRepo(t)
	repo.Run("update-ref", "refs/remotes/upstream/synthetic-work", "synthetic-main")
	// A branch whose name only begins with this one is a different branch.
	repo.Run("update-ref", "refs/remotes/mirror/synthetic-work/nested", "synthetic-main")

	refs, err := client.RemoteTracking(context.Background(), "synthetic-work")
	if err != nil {
		t.Fatalf("RemoteTracking() error = %v", err)
	}
	if !slices.Equal(refs, []string{"origin/synthetic-work", "upstream/synthetic-work"}) {
		t.Errorf("RemoteTracking() = %v", refs)
	}
}

func TestMoveBranchRefusesABranchThatMovedSinceItWasPlanned(t *testing.T) {
	repo, client := publishedRepo(t)
	ctx := context.Background()
	main, work := repo.Revision("synthetic-main"), repo.Revision("synthetic-work")

	if err := client.MoveBranch(ctx, "synthetic-main", work, work); err == nil {
		t.Fatal("MoveBranch() with a stale lease = nil, want update-ref's refusal")
	}
	if got := repo.Revision("synthetic-main"); got != main {
		t.Errorf("a refused move still moved synthetic-main to %s", got)
	}
	if err := client.MoveBranch(ctx, "synthetic-main", main, work); err != nil {
		t.Fatalf("MoveBranch() error = %v", err)
	}
	if got := repo.Revision("synthetic-main"); got != work {
		t.Errorf("synthetic-main = %s, want %s", got, work)
	}
}

// Git renames a branch another worktree has checked out and moves that
// worktree's HEAD with it, which is what makes a rename safe to allow there.
func TestRenameBranchCarriesAnotherWorktreeWithIt(t *testing.T) {
	repo, client := publishedRepo(t)
	repo.Run("switch", "-q", "synthetic-main")
	other := t.TempDir() + "/held"
	repo.Run("worktree", "add", "-q", other, "synthetic-work")

	if err := client.RenameBranch(context.Background(), "synthetic-work", "synthetic-renamed"); err != nil {
		t.Fatalf("RenameBranch() error = %v", err)
	}

	if head := runGitIn(t, other, "branch", "--show-current"); head != "synthetic-renamed" {
		t.Errorf("the other worktree is on %q, want synthetic-renamed", head)
	}
	if status := runGitIn(t, other, "status", "--porcelain"); status != "" {
		t.Errorf("the other worktree has changes nobody made:\n%s", status)
	}
}

func TestRenameBranchRefusesATakenName(t *testing.T) {
	_, client := publishedRepo(t)

	err := client.RenameBranch(context.Background(), "synthetic-work", "synthetic-main")
	if err == nil {
		t.Fatal("RenameBranch() onto an existing branch = nil, want git's refusal")
	}
}

func TestReshapeRefusesOptionLikeNames(t *testing.T) {
	_, client := publishedRepo(t)
	ctx := context.Background()

	_, unpublished := client.Unpublished(ctx, "--all", "synthetic-main")
	_, tracking := client.RemoteTracking(ctx, "-x")
	for name, err := range map[string]error{
		"Unpublished":    unpublished,
		"RemoteTracking": tracking,
		"MoveBranch":     client.MoveBranch(ctx, "synthetic-main", "--stdin", "synthetic-work"),
		"RenameBranch":   client.RenameBranch(ctx, "synthetic-work", "-D"),
	} {
		if err == nil || !strings.Contains(err.Error(), "cannot be passed safely") {
			t.Errorf("%s() error = %v, want a refusal", name, err)
		}
	}
}

func runGitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return gitExec(t, append([]string{"-C", dir}, args...)...)
}

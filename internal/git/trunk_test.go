package git

import (
	"context"
	"os"
	"testing"

	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
)

// A PATH fake proves nothing here: the question is what Git itself records
// under refs/remotes/<remote>/HEAD, and a fake answers whatever it is asked.
// Nothing leaves the machine — the "remote" is a second directory and every
// name is invented.
func TestDefaultBranchReadsWhatTheRemoteCallsItsDefault(t *testing.T) {
	origin := testutil.NewGitRepo(t, "synthetic-trunk")
	origin.Commit("synthetic root", "root.txt", "root")

	clone := testutil.NewGitRepo(t, "synthetic-placeholder")
	clone.Run("remote", "add", "origin", origin.Dir)
	clone.Run("fetch", "-q", "origin")
	clone.Run("remote", "set-head", "origin", "-a")

	inRepo(t, clone.Dir)
	branch, err := Client{Runner: subprocess.ExecRunner{}}.DefaultBranch(context.Background(), "origin")
	if err != nil {
		t.Fatalf("DefaultBranch() error = %v", err)
	}
	if branch != "synthetic-trunk" {
		t.Errorf("DefaultBranch() = %q, want the branch the remote calls its default", branch)
	}
}

// A repository nobody has told is the ordinary case for one built by hand, and
// it is not a failure. Returning an error would make every caller decide what
// to do about a question that simply has no answer here.
func TestAnUnsetDefaultIsAnEmptyAnswerNotAFailure(t *testing.T) {
	repo := testutil.NewGitRepo(t, "synthetic-trunk")
	repo.Commit("synthetic root", "root.txt", "root")

	inRepo(t, repo.Dir)
	branch, err := Client{Runner: subprocess.ExecRunner{}}.DefaultBranch(context.Background(), "origin")
	if err != nil {
		t.Errorf("DefaultBranch() error = %v for a repository with no remote", err)
	}
	if branch != "" {
		t.Errorf("DefaultBranch() = %q, want empty", branch)
	}
}

// The remote name reaches git as an argument, so it takes the same refusal
// every other name-shaped argument does.
func TestDefaultBranchRefusesAnOptionLikeRemote(t *testing.T) {
	if _, err := (Client{Runner: subprocess.ExecRunner{}}).DefaultBranch(context.Background(), "--synthetic-option"); err == nil {
		t.Error("DefaultBranch() error = nil for a remote git would read as an option")
	}
}

func inRepo(t *testing.T, dir string) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
}

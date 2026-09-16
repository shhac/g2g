package git

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
)

// Deleting a branch is one of the seams a PATH fake cannot vouch for: the fake
// answers whatever it is asked, and the questions here are what git will and
// will not remove.

// deletableRepo is a trunk and one branch built on it, with the checkout left
// on the branch.
func deletableRepo(t *testing.T) Client {
	t.Helper()
	repo := testutil.NewGitRepo(t, "synthetic-main")
	repo.Commit("synthetic root", "root.txt", "root")
	repo.Run("switch", "-qc", "synthetic-work")
	repo.Commit("synthetic work", "work.txt", "work")
	t.Chdir(repo.Dir)
	return Client{Runner: subprocess.ExecRunner{}}
}

func TestDeleteBranchRemovesABranchWhoseWorkWasSquashed(t *testing.T) {
	client := deletableRepo(t)
	ctx := context.Background()

	// The squash-merge shape: the work is in the trunk under a commit id the
	// branch never carried, which is exactly what git branch -d refuses.
	gitExec(t, "switch", "-q", "synthetic-main")
	gitExec(t, "merge", "-q", "--squash", "synthetic-work")
	gitExec(t, "commit", "-qm", "squashed synthetic-work")

	if err := client.DeleteBranch(ctx, "synthetic-work"); err != nil {
		t.Fatalf("DeleteBranch() error = %v", err)
	}
	branches, err := client.LocalBranches(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(branches, "synthetic-work") {
		t.Errorf("LocalBranches() = %v, want synthetic-work gone", branches)
	}
}

// Landing is re-entrant, so a second pass over a branch the first one deleted
// has reached the state it was asked for.
func TestDeleteBranchAcceptsABranchThatIsAlreadyGone(t *testing.T) {
	client := deletableRepo(t)

	if err := client.DeleteBranch(context.Background(), "synthetic-never-existed"); err != nil {
		t.Errorf("DeleteBranch() error = %v, want a missing branch to be no failure", err)
	}
}

// git will not delete the branch that is checked out, which is the ordinary
// ending of landing a whole stack from its top.
func TestDeleteBranchRefusesTheCheckedOutBranch(t *testing.T) {
	client := deletableRepo(t)

	err := client.DeleteBranch(context.Background(), "synthetic-work")
	if err == nil {
		t.Fatal("DeleteBranch() error = nil, want git's refusal to delete the checkout")
	}
}

func TestSwitchBranchMovesTheCheckoutSoTheBranchCanGo(t *testing.T) {
	client := deletableRepo(t)
	ctx := context.Background()

	if err := client.SwitchBranch(ctx, "synthetic-main"); err != nil {
		t.Fatalf("SwitchBranch() error = %v", err)
	}
	current, err := client.CurrentBranch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if current != "synthetic-main" {
		t.Errorf("CurrentBranch() = %q, want synthetic-main", current)
	}
	if err := client.DeleteBranch(ctx, "synthetic-work"); err != nil {
		t.Errorf("DeleteBranch() after switching away error = %v", err)
	}
}

func TestDeleteRefusesAnOptionLikeBranchName(t *testing.T) {
	client := deletableRepo(t)

	for name, err := range map[string]error{
		"DeleteBranch": client.DeleteBranch(context.Background(), "--all"),
		"SwitchBranch": client.SwitchBranch(context.Background(), "-f"),
	} {
		if err == nil || !strings.Contains(err.Error(), "cannot be passed safely") {
			t.Errorf("%s() error = %v, want a refusal", name, err)
		}
	}
}

// A remote that has already deleted the branch on merge is the ordinary case,
// not a failure, and it must not be distinguishable from having done it here.
func TestDeleteRemoteBranchAcceptsABranchTheRemoteNoLongerHas(t *testing.T) {
	arguments := filepath.Join(t.TempDir(), "git-arguments")
	t.Setenv("GIT_ARGUMENTS", arguments)
	testutil.WithFakeExecutables(t, map[string]string{
		"git": `printf '%s\n' "$*" >> "$GIT_ARGUMENTS"
case "$1" in
  remote) printf 'https://example.test/synthetic.git\n' ;;
  ls-remote) ;;
esac`,
	})

	if err := (Client{Runner: subprocess.ExecRunner{}}).DeleteRemoteBranch(context.Background(), "origin", "synthetic-gone"); err != nil {
		t.Fatalf("DeleteRemoteBranch() error = %v", err)
	}
	called, err := os.ReadFile(arguments)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(called), "push") {
		t.Errorf("pushed a delete for a branch the remote does not have: %q", called)
	}
}

func TestDeleteRemoteBranchPushesADeleteForABranchTheRemoteHas(t *testing.T) {
	arguments := filepath.Join(t.TempDir(), "git-arguments")
	t.Setenv("GIT_ARGUMENTS", arguments)
	testutil.WithFakeExecutables(t, map[string]string{
		"git": `printf '%s\n' "$*" >> "$GIT_ARGUMENTS"
case "$1" in
  remote) printf 'https://example.test/synthetic.git\n' ;;
  ls-remote) printf '1111111111111111111111111111111111111111\trefs/heads/synthetic-landed\n' ;;
esac`,
	})

	if err := (Client{Runner: subprocess.ExecRunner{}}).DeleteRemoteBranch(context.Background(), "origin", "synthetic-landed"); err != nil {
		t.Fatalf("DeleteRemoteBranch() error = %v", err)
	}
	called, err := os.ReadFile(arguments)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(called), "push origin --delete synthetic-landed") {
		t.Errorf("git calls missing the delete push: %q", called)
	}
}

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

	if err := (Client{Runner: subprocess.ExecRunner{}}).DeleteRemoteBranch(context.Background(), "origin", "synthetic-gone", "1111111111111111111111111111111111111111"); err != nil {
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

func TestDeleteRemoteBranchPushesALeasedDeleteForABranchTheRemoteHas(t *testing.T) {
	arguments := filepath.Join(t.TempDir(), "git-arguments")
	t.Setenv("GIT_ARGUMENTS", arguments)
	testutil.WithFakeExecutables(t, map[string]string{
		"git": `printf '%s\n' "$*" >> "$GIT_ARGUMENTS"
case "$1" in
  remote) printf 'https://example.test/synthetic.git\n' ;;
  ls-remote) printf '1111111111111111111111111111111111111111\trefs/heads/synthetic-landed\n' ;;
esac`,
	})

	if err := (Client{Runner: subprocess.ExecRunner{}}).DeleteRemoteBranch(context.Background(), "origin", "synthetic-landed", "1111111111111111111111111111111111111111"); err != nil {
		t.Fatalf("DeleteRemoteBranch() error = %v", err)
	}
	called, err := os.ReadFile(arguments)
	if err != nil {
		t.Fatal(err)
	}
	if want := "push --force-with-lease=refs/heads/synthetic-landed:1111111111111111111111111111111111111111 origin --delete refs/heads/synthetic-landed"; !strings.Contains(string(called), want) {
		t.Errorf("git calls = %q, want the leased delete %q", called, want)
	}
}

// The deletion is only ever of the head that landed. Without one there is
// nothing to pin a lease to, and a delete without a lease is the bug.
func TestDeleteRemoteBranchRefusesWithoutALandedHead(t *testing.T) {
	arguments := filepath.Join(t.TempDir(), "git-arguments")
	t.Setenv("GIT_ARGUMENTS", arguments)
	testutil.WithFakeExecutables(t, map[string]string{
		"git": `printf '%s\n' "$*" >> "$GIT_ARGUMENTS"
case "$1" in
  remote) printf 'https://example.test/synthetic.git\n' ;;
  ls-remote) printf '1111111111111111111111111111111111111111\trefs/heads/synthetic-landed\n' ;;
esac`,
	})

	if err := (Client{Runner: subprocess.ExecRunner{}}).DeleteRemoteBranch(context.Background(), "origin", "synthetic-landed", ""); err == nil {
		t.Fatal("DeleteRemoteBranch() error = nil, want a refusal without a landed head")
	}
	called, _ := os.ReadFile(arguments)
	if strings.Contains(string(called), "push") {
		t.Errorf("pushed a delete with no landed head: %q", called)
	}
}

// What a fake cannot say is whether git honours the lease on a delete, so the
// rest of these run against a real bare remote.

// recordingRunner runs git for real, remembering each invocation, and lets a
// test act just before one starts -- which is where a reviewer's push lands
// between the read and the write.
type recordingRunner struct {
	calls  [][]string
	before func(args []string)
}

func (r *recordingRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, args)
	if r.before != nil {
		r.before(args)
	}
	return subprocess.ExecRunner{}.Run(ctx, name, args...)
}

func (r *recordingRunner) pushes() [][]string {
	var pushes [][]string
	for _, call := range r.calls {
		if call[0] == "push" {
			pushes = append(pushes, call)
		}
	}
	return pushes
}

// publishedBranch is a branch with one commit of its own, pushed to the
// synthetic remote, and its tip.
func publishedBranch(t *testing.T, branch string) string {
	t.Helper()
	gitExec(t, "switch", "-qc", branch)
	if err := os.WriteFile(branch+".txt", []byte(branch), 0o600); err != nil {
		t.Fatal(err)
	}
	gitExec(t, "add", "-A")
	gitExec(t, "commit", "-qm", "synthetic "+branch)
	gitExec(t, "push", "-q", "origin", branch)
	gitExec(t, "switch", "-q", "synthetic-trunk")
	return revision(t, branch)
}

func remoteTip(t *testing.T, client Client, branch string) string {
	t.Helper()
	tips, err := client.RemoteTips(context.Background(), "origin", []string{branch})
	if err != nil {
		t.Fatal(err)
	}
	return tips[branch]
}

func TestDeleteRemoteBranchDeletesTheHeadThatLanded(t *testing.T) {
	_, client := syntheticRemote(t)
	landed := publishedBranch(t, "synthetic-landed")
	runner := &recordingRunner{}
	client.Runner = runner

	if err := client.DeleteRemoteBranch(context.Background(), "origin", "synthetic-landed", landed); err != nil {
		t.Fatalf("DeleteRemoteBranch() error = %v", err)
	}
	if tip := remoteTip(t, client, "synthetic-landed"); tip != "" {
		t.Errorf("remote still has synthetic-landed at %s", tip)
	}
	want := []string{"push", "--force-with-lease=refs/heads/synthetic-landed:" + landed, "origin", "--delete", "refs/heads/synthetic-landed"}
	if pushes := runner.pushes(); len(pushes) != 1 || !slices.Equal(pushes[0], want) {
		t.Errorf("pushes = %q, want exactly %q", pushes, want)
	}
}

// A reviewer pushed after the merge. The branch is no longer the head that
// landed, and their commit exists nowhere else.
func TestDeleteRemoteBranchLeavesABranchThatMovedAfterItLanded(t *testing.T) {
	upstream, client := syntheticRemote(t)
	landed := publishedBranch(t, "synthetic-landed")
	reviewed := advanceUpstream(t, upstream, "synthetic-landed", "review.txt")
	runner := &recordingRunner{}
	client.Runner = runner

	err := client.DeleteRemoteBranch(context.Background(), "origin", "synthetic-landed", landed)
	if err == nil || !strings.Contains(err.Error(), "left in place") {
		t.Fatalf("DeleteRemoteBranch() error = %v, want the moved branch left in place", err)
	}
	if tip := remoteTip(t, client, "synthetic-landed"); tip != reviewed {
		t.Errorf("remote synthetic-landed = %q, want the reviewer's %s kept", tip, reviewed)
	}
	if pushes := runner.pushes(); len(pushes) != 0 {
		t.Errorf("pushes = %q, want none", pushes)
	}
}

// The same push arriving after the remote was read and before the delete
// reaches it. Only the lease stands between that commit and the delete.
func TestDeleteRemoteBranchLeaseHoldsAgainstAPushAfterItLooked(t *testing.T) {
	upstream, client := syntheticRemote(t)
	landed := publishedBranch(t, "synthetic-landed")
	reviewed := ""
	runner := &recordingRunner{before: func(args []string) {
		if args[0] == "push" && reviewed == "" {
			reviewed = advanceUpstream(t, upstream, "synthetic-landed", "review.txt")
		}
	}}
	client.Runner = runner

	if err := client.DeleteRemoteBranch(context.Background(), "origin", "synthetic-landed", landed); err == nil {
		t.Fatal("DeleteRemoteBranch() error = nil, want the lease to refuse")
	}
	if tip := remoteTip(t, client, "synthetic-landed"); reviewed == "" || tip != reviewed {
		t.Errorf("remote synthetic-landed = %q, want the reviewer's %q kept", tip, reviewed)
	}
	want := []string{"push", "--force-with-lease=refs/heads/synthetic-landed:" + landed, "origin", "--delete", "refs/heads/synthetic-landed"}
	if pushes := runner.pushes(); len(pushes) != 1 || !slices.Equal(pushes[0], want) {
		t.Errorf("pushes = %q, want exactly %q", pushes, want)
	}
}

func TestDeleteRemoteBranchAcceptsABranchARealRemoteNeverHad(t *testing.T) {
	_, client := syntheticRemote(t)
	runner := &recordingRunner{}
	client.Runner = runner

	if err := client.DeleteRemoteBranch(context.Background(), "origin", "synthetic-gone", "1111111111111111111111111111111111111111"); err != nil {
		t.Fatalf("DeleteRemoteBranch() error = %v, want a missing branch to be no failure", err)
	}
	if pushes := runner.pushes(); len(pushes) != 0 {
		t.Errorf("pushes = %q, want none", pushes)
	}
}

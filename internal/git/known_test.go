package git

import (
	"context"
	"errors"
	"os/exec"
	"testing"
)

// After a pull, g2g's own ref has moved on and the remote-tracking ref has
// not, so the trunk read from the remote-tracking ref alone would be behind by
// exactly what was just pulled. The later of the two answers, and neither ref
// moves to give it.
func TestKnownTipsTakesTheLaterOfTheTwoRefs(t *testing.T) {
	upstream, client := syntheticRemote(t)
	before := revision(t, "refs/remotes/origin/synthetic-trunk")
	moved := advanceUpstream(t, upstream, "synthetic-trunk", "remote.txt")
	if err := client.FetchIsolated(context.Background(), "origin", []string{"synthetic-trunk"}); err != nil {
		t.Fatal(err)
	}

	tips, err := client.KnownTips(context.Background(), "origin", []string{"synthetic-trunk", "synthetic-absent"})
	if err != nil {
		t.Fatalf("KnownTips() error = %v", err)
	}
	if tips["synthetic-trunk"] != moved {
		t.Errorf("tip = %q, want the fetched %q", tips["synthetic-trunk"], moved)
	}
	if _, present := tips["synthetic-absent"]; present {
		t.Error("a branch the remote was never seen with should be absent")
	}
	if after := revision(t, "refs/remotes/origin/synthetic-trunk"); after != before {
		t.Errorf("remote-tracking ref moved from %s to %s", before, after)
	}
}

// Rewritten since the fetch, and pushed: the two refs are no longer in order,
// and the push is what the remote-tracking ref recorded last.
func TestKnownTipsPrefersTheRemoteTrackingRefWhenTheyAreNotInOrder(t *testing.T) {
	upstream, client := syntheticRemote(t)
	advanceUpstream(t, upstream, "synthetic-trunk", "remote.txt")
	if err := client.FetchIsolated(context.Background(), "origin", []string{"synthetic-trunk"}); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "commit", "-q", "--allow-empty", "-m", "synthetic rewrite").CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, output)
	}
	if output, err := exec.Command("git", "push", "-q", "--force", "origin", "synthetic-trunk").CombinedOutput(); err != nil {
		t.Fatalf("push: %v\n%s", err, output)
	}
	pushed := revision(t, "refs/remotes/origin/synthetic-trunk")

	tips, err := client.KnownTips(context.Background(), "origin", []string{"synthetic-trunk"})
	if err != nil {
		t.Fatalf("KnownTips() error = %v", err)
	}
	if tips["synthetic-trunk"] != pushed {
		t.Errorf("tip = %q, want the pushed %q", tips["synthetic-trunk"], pushed)
	}
}

func TestKnownTipsRefusesARemoteThatDoesNotExist(t *testing.T) {
	_, client := syntheticRemote(t)
	if _, err := client.KnownTips(context.Background(), "synthetic-nowhere", []string{"synthetic-trunk"}); !errors.Is(err, ErrNoSuchRemote) {
		t.Errorf("KnownTips() error = %v, want ErrNoSuchRemote", err)
	}
}

// A branch merged and deleted on the remote goes from the remote-tracking refs
// with the next pruning fetch, and is left behind in g2g's own, which is only
// ever fetched. Read from that, it stayed published for good.
func TestKnownTipsForgetsABranchTheRemoteDeleted(t *testing.T) {
	upstream, client := syntheticRemote(t)
	for _, args := range [][]string{
		{"switch", "-q", "-c", "synthetic/topic"},
		{"commit", "-q", "--allow-empty", "-m", "synthetic topic"},
		{"push", "-q", "origin", "synthetic/topic"},
	} {
		if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	if err := client.FetchIsolated(context.Background(), "origin", []string{"synthetic/topic"}); err != nil {
		t.Fatal(err)
	}
	tips, err := client.KnownTips(context.Background(), "origin", []string{"synthetic/topic"})
	if err != nil || tips["synthetic/topic"] == "" {
		t.Fatalf("KnownTips() = %v, %v; want the pushed branch with a slash in its name", tips, err)
	}

	if output, err := exec.Command("git", "--git-dir", upstream, "branch", "-D", "synthetic/topic").CombinedOutput(); err != nil {
		t.Fatalf("delete upstream: %v\n%s", err, output)
	}
	if output, err := exec.Command("git", "fetch", "-q", "--prune", "origin").CombinedOutput(); err != nil {
		t.Fatalf("fetch: %v\n%s", err, output)
	}
	tips, err = client.KnownTips(context.Background(), "origin", []string{"synthetic/topic"})
	if err != nil {
		t.Fatalf("KnownTips() error = %v", err)
	}
	if tip, present := tips["synthetic/topic"]; present {
		t.Errorf("a branch the remote deleted is still known at %s", tip)
	}
}

// What sync already has decides what it needs to fetch, so the answer has to
// name each branch — slashes and all — at exactly the commit fetched.
func TestIsolatedTipsNamesWhatWasFetched(t *testing.T) {
	upstream, client := syntheticRemote(t)
	moved := advanceUpstream(t, upstream, "synthetic-trunk", "remote.txt")
	for _, args := range [][]string{
		{"switch", "-q", "-c", "synthetic/topic"},
		{"commit", "-q", "--allow-empty", "-m", "synthetic topic"},
		{"push", "-q", "origin", "synthetic/topic"},
	} {
		if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	if err := client.FetchIsolated(context.Background(), "origin", []string{"synthetic-trunk", "synthetic/topic"}); err != nil {
		t.Fatal(err)
	}

	tips, err := client.IsolatedTips(context.Background(), "origin")
	if err != nil {
		t.Fatalf("IsolatedTips() error = %v", err)
	}
	if tips["synthetic-trunk"] != moved || tips["synthetic/topic"] != revision(t, "synthetic/topic") || len(tips) != 2 {
		t.Errorf("IsolatedTips() = %v, want the trunk at %s and synthetic/topic", tips, moved)
	}
}

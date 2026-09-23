package git

import (
	"context"
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
	if _, err := client.KnownTips(context.Background(), "synthetic-nowhere", []string{"synthetic-trunk"}); err == nil {
		t.Error("KnownTips() error = nil for a remote that is not configured")
	}
}

package cli_test

import (
	"strings"
	"testing"
)

// syncedTip is the sync point g2g holds in a clone for a branch on origin,
// empty when it holds none.
func (w *world) syncedTip(dir, branch string) string {
	w.t.Helper()
	return w.git(dir, "for-each-ref", "--format=%(objectname)", "refs/g2g/synced/origin/"+branch)
}

// Every command that leaves this clone and the remote agreeing on a branch
// records where: push, pull and an adoption of a published stack. Deleting a
// branch forgets it and renaming carries it, so a branch made again under the
// old name starts with none.
func TestSyncPointsFollowTheBranchesTheyDescribe(t *testing.T) {
	s := newSharedStack(t)
	// Alice pushed the stack and has since moved it on locally; nobody has
	// published since, so her agreement is the remote as it stands.
	for _, branch := range []string{"synthetic-a", "synthetic-b", "synthetic-c"} {
		if got, want := s.syncedTip(s.Local, branch), s.tip(s.Remote, branch); got != want {
			t.Errorf("Alice's push left %s's sync point at %q, want the published tip %q", branch, got, want)
		}
	}
	// Bob adopted the published chain while level with it, then moved B and C
	// on locally: the agreement is still where it was.
	s.asBob()
	if got, want := s.syncedTip(s.Other, "synthetic-a"), s.tip(s.Other, "origin/synthetic-a"); got != want {
		t.Errorf("Bob's adoption recorded synthetic-a at %q, want %q", got, want)
	}

	mustRun(t, "push", "--apply")
	if got, want := s.syncedTip(s.Other, "synthetic-b"), s.tip(s.Other, "synthetic-b"); got != want {
		t.Errorf("Bob's push recorded synthetic-b at %q, want the pushed tip %q", got, want)
	}

	s.asAlice()
	mustRun(t, "pull", "--apply")
	if got, want := s.syncedTip(s.Local, "synthetic-b"), s.tip(s.Remote, "synthetic-b"); got != want {
		t.Errorf("Alice's pull recorded synthetic-b at %q, want what she pulled, %q", got, want)
	}
	history := s.git(s.Local, "reflog", "show", "--format=%gs", "refs/g2g/synced/origin/synthetic-b")
	if !strings.Contains(history, "g2g pull local=") || !strings.Contains(history, "g2g push local=") {
		t.Errorf("the sync point's history does not keep each agreement:\n%s", history)
	}

	mustRun(t, "rename", "--branch", "synthetic-c", "synthetic-renamed", "--apply")
	if s.syncedTip(s.Local, "synthetic-c") != "" || s.syncedTip(s.Local, "synthetic-renamed") == "" {
		t.Error("the sync point did not follow the rename")
	}
	mustRun(t, "delete", "--branch", "synthetic-renamed", "--apply")
	if s.syncedTip(s.Local, "synthetic-renamed") != "" {
		t.Error("deleting the branch left its sync point behind")
	}
}

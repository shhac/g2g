package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/cli"
)

// A replay that resolves a conflict changes each conflicting commit's patch,
// so by content none of them matches what was published. They are still the
// commits both clones agreed on, replayed: push publishes them, and a pull on
// the other clone takes them, rather than reading each as one commit dropped
// and an unrelated one made -- which refused a branch whose every commit had
// conflicted, and named the rest as dropped.

// resolveReplay puts a commit on A, in dir, that touches what x and y on B
// touch, then restacks B onto it as that clone and resolves every conflict
// the way a person would, until the restack finishes.
func (s sharedStack) resolveReplay(dir string) {
	s.t.Helper()
	s.git(dir, "switch", "-q", "synthetic-a")
	for _, name := range []string{"x.txt", "y.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("from a\n"), 0o600); err != nil {
			s.t.Fatal(err)
		}
	}
	s.git(dir, "add", "-A")
	s.git(dir, "commit", "-qm", "synthetic a touches x and y")
	s.git(dir, "switch", "-q", "synthetic-b")
	stdout, _, err := run(s.t, "restack", "--apply")
	for stops := 0; cli.StoppedPartWayForTest(err); stops++ {
		if stops == 3 {
			s.t.Fatalf("the restack kept stopping:\n%s", stdout)
		}
		for _, name := range strings.Fields(s.git(dir, "diff", "--name-only", "--diff-filter=U")) {
			if err := os.WriteFile(filepath.Join(dir, name), []byte("resolved\n"), 0o600); err != nil {
				s.t.Fatal(err)
			}
			s.git(dir, "add", name)
		}
		stdout, _, err = run(s.t, "restack", "--continue")
	}
	if err != nil {
		s.t.Fatalf("restack: %v\n%s", err, stdout)
	}
	s.assertClean(dir)
}

// Every commit of Alice's B conflicted, so none of it matches the remote by
// content, which push used to refuse as a branch emptied or made again,
// handing her a raw git push. Her sync point says the remote holds exactly
// what she last agreed on, and each commit is one of those replayed.
func TestJourneyPushPublishesAReplayThatResolvedConflicts(t *testing.T) {
	s := newDropWorld(t)
	s.resolveReplay(s.Local)

	if status := mustRun(t, "status"); !strings.Contains(status, "2 changed in the replay") {
		t.Errorf("status does not say the replay changed two commits:\n%s", status)
	}
	preview := mustRun(t, "push")
	for _, want := range []string{"2 commits changed while resolving the replay", "Replaces 2 published commits", "synthetic x.txt", "synthetic y.txt"} {
		if !strings.Contains(preview, want) {
			t.Errorf("the push preview does not say %q:\n%s", want, preview)
		}
	}
	if strings.Contains(preview, "Drops") || strings.Contains(preview, "force-with-lease=") {
		t.Errorf("the push preview reads the replay as a drop:\n%s", preview)
	}
	mustRun(t, "push", "--apply")
	if s.tip(s.Remote, "synthetic-b") != s.tip(s.Local, "synthetic-b") {
		t.Error("the remote does not hold Alice's resolved B")
	}
	if got := s.git(s.Remote, "show", "synthetic-b:x.txt"); got != "resolved" {
		t.Errorf("the remote's x.txt = %q, want the resolution", got)
	}
	s.assertClean(s.Local)
	if status := mustRun(t, "status"); strings.Contains(status, "dropped") {
		t.Errorf("status names a replaced commit as dropped:\n%s", status)
	}
}

// Bob amended y on the remote while Alice resolved her replay. His amend
// keeps y's author, date and subject, but it is not a commit Alice ever
// agreed on, so nothing pairs with it and her push refuses rather than
// overwrite it.
func TestJourneyPushStillRefusesOverAReviewersAmend(t *testing.T) {
	s := newDropWorld(t)
	s.resolveReplay(s.Local)
	s.asBob()
	s.git(s.Other, "switch", "-q", "synthetic-b")
	if err := os.WriteFile(filepath.Join(s.Other, "y.txt"), []byte("bob's fix\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.git(s.Other, "commit", "-qa", "--amend", "--no-edit")
	s.git(s.Other, "push", "-q", "--force", "origin", "synthetic-b")
	amended := s.tip(s.Remote, "synthetic-b")

	s.asAlice()
	mustRun(t, "pull") // the preview fetches, so Bob's amend is here to compare
	if stdout, _, err := run(t, "push", "--apply"); err == nil {
		t.Fatalf("Alice's push replaced Bob's amend:\n%s", stdout)
	}
	if s.tip(s.Remote, "synthetic-b") != amended {
		t.Error("Bob's amend is gone from the remote")
	}
}

// Bob resolved the conflicts and published. Every commit of B changed, which
// pull used to refuse as a branch the remote emptied; it takes Bob's replay,
// naming each commit it replaces.
func TestJourneyPullTakesAReplayThatResolvedConflicts(t *testing.T) {
	s := newDropWorld(t)
	s.asBob()
	s.resolveReplay(s.Other)
	mustRun(t, "push", "--apply")

	s.asAlice()
	preview := mustRun(t, "pull")
	if !strings.Contains(preview, "Takes origin's versions of 2 commits") {
		t.Errorf("the pull preview does not name the replaced commits:\n%s", preview)
	}
	// A changed patch is a change to what the branch holds, which --strict
	// is for.
	if stdout, _, err := run(t, "pull", "--strict", "--apply"); err == nil {
		t.Errorf("pull --strict took a replay whose patches changed:\n%s", stdout)
	}
	if stdout, _, err := run(t, "pull", "--apply"); err != nil {
		t.Fatalf("Alice's pull: %v\n%s", err, stdout)
	}
	if s.tip(s.Local, "synthetic-b") != s.tip(s.Remote, "synthetic-b") {
		t.Error("Alice's B is not Bob's resolved replay")
	}
	s.assertClean(s.Local)
	if got := s.git(s.Local, "show", "synthetic-b:y.txt"); got != "resolved" {
		t.Errorf("Alice's y.txt = %q, want Bob's resolution", got)
	}
	if stdout, _, err := run(t, "pull", "--strict"); err != nil || strings.Contains(stdout, "Apply blocked") {
		t.Errorf("a second pull still has something to say: %v\n%s", err, stdout)
	}
}

// Bob resolved the conflicts and published, and Alice, who has not taken his
// replay, commits on B and pushes. Bob's replayed commits are ones she never
// agreed on, so publishing hers over them would erase his resolution: her
// push refuses, and pull is what takes his.
func TestJourneyPushRefusesOverAColleaguesReplayItHasNotTaken(t *testing.T) {
	s := newDropWorld(t)
	s.asBob()
	s.resolveReplay(s.Other)
	mustRun(t, "push", "--apply")
	replayed := s.tip(s.Remote, "synthetic-b")

	s.asAlice()
	s.commit(s.Local, "synthetic-b", "alice.txt", "alice's work")
	mustRun(t, "pull") // the preview fetches, so Bob's replay is here to compare
	stdout, _, err := run(t, "push", "--branch", "synthetic-b", "--scope", "branch", "--apply")
	if err == nil {
		t.Fatalf("Alice's push replaced Bob's resolved replay:\n%s", stdout)
	}
	// Refused as diverged, which is what names a replay as one way the
	// published version came to differ.
	if !strings.Contains(stdout, "a conflict-resolved replay can also change the published patch") {
		t.Errorf("the refusal is not the divergence one:\n%s", stdout)
	}
	if s.tip(s.Remote, "synthetic-b") != replayed {
		t.Error("Bob's resolved replay is gone from the remote")
	}
	s.assertClean(s.Local)
}

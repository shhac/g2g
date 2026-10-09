package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/cli"
)

// shared stack: two people working on one published stack, each from their
// own clone, each running g2g. Alice publishes main < A < B < C; Bob adopts
// the chain, commits to B and restacks; Alice commits to A and restacks. Each
// order of publishing first has to end with the second person pulling and
// pushing, and the remote holding both people's work.
//
// Local is Alice's clone and Other is Bob's. g2g runs in-process, so standing
// in a clone is changing directory into it.
type sharedStack struct {
	*world
}

func newSharedStack(t *testing.T) sharedStack {
	t.Helper()
	return newSharedStackEditing(t, "a2.txt", "b2.txt")
}

// newSharedStackEditing is the same stack with Alice's commit on A writing
// aliceFile and Bob's on B writing bobFile, so a test can make them collide.
func newSharedStackEditing(t *testing.T, aliceFile, bobFile string) sharedStack {
	t.Helper()
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	w.branchOff("synthetic-b", "synthetic-c", "c.txt")
	mustRun(t, "adopt", "--trunk", "main", "--apply")
	mustRun(t, "push", "--apply")

	s := sharedStack{w}
	s.asBob()
	w.git(w.Other, "fetch", "-q", "origin")
	for _, branch := range []string{"synthetic-a", "synthetic-b", "synthetic-c"} {
		w.git(w.Other, "branch", "-q", branch, "origin/"+branch)
	}
	w.git(w.Other, "switch", "-q", "synthetic-c")
	mustRun(t, "adopt", "--scope", "path", "--trunk", "main", "--apply")
	w.commit(w.Other, "synthetic-b", bobFile, "bob's")
	mustRun(t, "restack", "--apply")

	s.asAlice()
	w.commit(w.Local, "synthetic-a", aliceFile, "alice's")
	mustRun(t, "restack", "--apply")
	return s
}

func (s sharedStack) asAlice() { s.t.Chdir(s.Local) }
func (s sharedStack) asBob()   { s.t.Chdir(s.Other) }

// assertBothPublished checks the remote, not either clone's account of it:
// every branch carries both people's work, stacked as it was recorded.
func (s sharedStack) assertBothPublished() {
	s.t.Helper()
	remote := s.Remote
	for branch, files := range map[string][]string{
		"synthetic-a": {"a.txt", "a2.txt"},
		"synthetic-b": {"a2.txt", "b.txt", "b2.txt"},
		"synthetic-c": {"a2.txt", "b2.txt", "c.txt"},
	} {
		for _, file := range files {
			s.assertHas(remote, branch, file)
		}
	}
	for _, pair := range [][2]string{{"synthetic-a", "synthetic-b"}, {"synthetic-b", "synthetic-c"}} {
		if !s.contains(remote, pair[0], pair[1]) {
			s.t.Errorf("on the remote %s is not built on %s", pair[1], pair[0])
		}
	}
}

// Bob publishes first. Alice's push is refused, because her B lacks Bob's
// commit; her pull takes Bob's B and replays it onto her newer A.
func TestJourneySharedStackAlicePullsAfterBobPublished(t *testing.T) {
	s := newSharedStack(t)
	s.asBob()
	mustRun(t, "push", "--apply")

	s.asAlice()
	if _, _, err := run(t, "push", "--apply"); err == nil {
		t.Fatal("Alice's push overwrote Bob's commit")
	}
	if stdout, _, err := run(t, "pull", "--apply"); err != nil {
		t.Fatalf("Alice's pull: %v\n%s", err, stdout)
	}
	s.assertClean(s.Local)
	if stdout, _, err := run(t, "push", "--apply"); err != nil {
		t.Fatalf("Alice's push after pulling: %v\n%s", err, stdout)
	}
	s.assertBothPublished()
}

// Alice publishes first. Bob's push is refused, because his stack lacks
// Alice's commit; his pull takes her A and replays his B onto it. Her commit
// on A is the parent's, not B's, so B is not "both moved".
func TestJourneySharedStackBobPullsAfterAlicePublished(t *testing.T) {
	s := newSharedStack(t)
	mustRun(t, "push", "--apply")

	s.asBob()
	if _, _, err := run(t, "push", "--apply"); err == nil {
		t.Fatal("Bob's push overwrote Alice's commit")
	}
	stdout, _, err := run(t, "pull", "--apply")
	if err != nil {
		t.Fatalf("Bob's pull: %v\n%s", err, stdout)
	}
	if strings.Contains(stdout, "both sides have moved") {
		t.Errorf("Alice's commit on the parent read as a divergence of synthetic-b:\n%s", stdout)
	}
	s.assertClean(s.Other)
	if stdout, _, err := run(t, "push", "--apply"); err != nil {
		t.Fatalf("Bob's push after pulling: %v\n%s", err, stdout)
	}
	s.assertBothPublished()
}

// Alice's pull takes Bob's B and replays it onto her A, and the two commits
// collide. She resolves it and continues: the continue plans again from the
// record, so the record has to say where Bob's version of B begins.
func TestJourneySharedStackAConflictingPullCanBeContinued(t *testing.T) {
	s := newSharedStackEditing(t, "shared.txt", "shared.txt")
	s.asBob()
	mustRun(t, "push", "--apply")

	s.asAlice()
	stdout, _, err := run(t, "pull", "--apply")
	if !cli.StoppedPartWayForTest(err) {
		t.Fatalf("pull error = %v, want it stopped on the conflict:\n%s", err, stdout)
	}
	if err := os.WriteFile(filepath.Join(s.Local, "shared.txt"), []byte("both\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.git(s.Local, "add", "shared.txt")
	if stdout, _, err := run(t, "restack", "--continue"); err != nil {
		t.Fatalf("restack --continue: %v\n%s", err, stdout)
	}
	s.assertClean(s.Local)
	for _, file := range []string{"a.txt", "b.txt", "shared.txt"} {
		s.assertHas(s.Local, "synthetic-c", file)
	}
	if !s.contains(s.Local, "synthetic-a", "synthetic-b") || !s.contains(s.Local, "synthetic-b", "synthetic-c") {
		t.Error("the continued replay did not build the stack in order")
	}
	// Resolving rewrote Bob's commit, so his version is no longer in hers by
	// content, and push says so rather than replacing it unasked.
	stdout, _, err = run(t, "push", "--apply")
	if err == nil || !strings.Contains(stdout+err.Error(), "conflict-resolved replay") {
		t.Errorf("push after a resolved conflict: error = %v, want the resolved patch named\n%s", err, stdout)
	}
}

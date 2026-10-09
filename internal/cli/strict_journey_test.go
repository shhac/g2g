package cli_test

import (
	"strings"
	"testing"
)

// --strict: stop rather than plough on. Everything these commands do by
// default with a drop -- publish it, take it -- is refused by name instead,
// and so is a branch that differs from the remote with nothing to say whose
// the difference is. What is in step goes through.
func TestJourneyStrictRefusesWhatIsOutOfStepAndOnlyThat(t *testing.T) {
	s := newDropWorld(t)

	// In step: an ordinary commit on a branch with a sync point.
	s.commit(s.Local, "synthetic-a", "more.txt", "more")
	mustRun(t, "restack", "--apply")
	if stdout, _, err := run(t, "push", "--strict", "--apply"); err != nil {
		t.Fatalf("push --strict of an ordinary commit: %v\n%s", err, stdout)
	}

	// Alice drops x: the default push publishes it; --strict refuses.
	x := s.dropX(s.Local)
	stdout, _, err := run(t, "push", "--strict", "--apply")
	if err == nil || !strings.Contains(stdout+err.Error(), "--strict: it would drop synthetic-b "+x[:12]) {
		t.Errorf("push --strict of a drop: error = %v\n%s", err, stdout)
	}
	if !s.contains(s.Remote, x, "synthetic-b") {
		t.Error("a strictly refused push published the drop")
	}
	mustRun(t, "push", "--apply")

	// A branch with no record of where it last agreed with the remote cannot
	// be said to be in step, even when it is only ahead.
	s.git(s.Local, "update-ref", "-d", "refs/g2g/synced/origin/synthetic-a")
	s.commit(s.Local, "synthetic-a", "ahead.txt", "ahead")
	stdout, _, err = run(t, "push", "--branch", "synthetic-a", "--scope", "branch", "--strict")
	if err != nil || !strings.Contains(stdout, "synthetic-a differs from origin with no record of where the two last agreed") {
		t.Errorf("push --strict of a branch with no sync point: error = %v\n%s", err, stdout)
	}

	// Bob, who still has x under its original id, pulls: the default drops
	// it here too; --strict refuses.
	s.asBob()
	bobs := s.tip(s.Other, "synthetic-b~1")
	stdout, _, err = run(t, "pull", "--strict", "--apply")
	if err == nil || !strings.Contains(stdout+err.Error(), "--strict: it would drop synthetic-b "+bobs[:12]) {
		t.Errorf("pull --strict of a drop: error = %v\n%s", err, stdout)
	}
	if !s.contains(s.Other, bobs, "synthetic-b") {
		t.Error("a strictly refused pull dropped the commit")
	}

	if _, _, err := run(t, "pull", "--strict", "--take", "published"); err == nil {
		t.Error("pull accepted --strict with --take, which discards on purpose")
	}
}

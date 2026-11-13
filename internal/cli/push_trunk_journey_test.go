package cli_test

import (
	"strings"
	"testing"
)

// A declared trunk that deploys from pushes -- a staging branch -- is
// published by publishing the branch itself. push publishes stacks, and its
// only advice was to start a stack on the trunk, which publishes nothing that
// deploys. The refusal stays; it now names the push that does what was meant.
func TestJourneyPushingADeclaredTrunkNamesThePushThatPublishesIt(t *testing.T) {
	w := newWorld(t)
	w.git(w.Local, "switch", "-qc", "synthetic-staging")
	w.git(w.Local, "push", "-q", "origin", "synthetic-staging")
	w.commit(w.Local, "synthetic-staging", "reviewed.txt", "a reviewed fix")
	mustRun(t, "track", "--branch", "synthetic-staging", "--as-trunk", "--apply")

	_, _, err := run(t, "push", "--branch", "synthetic-staging", "--scope", "branch")
	if err == nil || !strings.Contains(err.Error(), "git push origin synthetic-staging") {
		t.Errorf("pushing a declared trunk = %v, want the refusal to name git push", err)
	}
	if w.tip(w.Remote, "synthetic-staging") == w.tip(w.Local, "synthetic-staging") {
		t.Error("the refusal published the trunk")
	}
}

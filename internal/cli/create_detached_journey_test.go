package cli_test

import (
	"strings"
	"testing"
)

// Detached at the tip of a stack that has landed, someone asks for a new
// branch at the fetched trunk with --at, which the refusal itself suggests,
// and was refused again: HEAD was "somewhere other than main's tip". Nothing
// would have been left behind. What matters is whether anything only HEAD
// reaches is work the start does not already have.
func TestJourneyCreateFromADetachedHeadThatLosesNothing(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-landed", "landed.txt")
	mustRun(t, "track", "--parent", "main", "--apply")
	// The stack squash-merged upstream, and its branch deleted here.
	w.git(w.Other, "pull", "-q")
	w.commit(w.Other, "main", "landed.txt", "landed.txt")
	w.git(w.Other, "push", "-q", "origin", "main")
	w.git(w.Local, "fetch", "-q", "origin")

	w.git(w.Local, "switch", "-q", "--detach", "synthetic-landed")
	mustRun(t, "create", "synthetic-still-there", "--parent", "main", "--at", "origin/main", "--apply")
	w.assertClean(w.Local)

	w.git(w.Local, "switch", "-q", "--detach", "synthetic-landed")
	w.git(w.Local, "branch", "-q", "-D", "synthetic-landed")
	mustRun(t, "create", "synthetic-squashed", "--parent", "main", "--at", "origin/main", "--apply")
	w.assertClean(w.Local)
	if got := w.tip(w.Local, "synthetic-squashed"); got != w.tip(w.Local, "origin/main") {
		t.Errorf("synthetic-squashed starts at %s, want origin/main", got)
	}

	// Work only HEAD has is still refused, with the way to keep it.
	w.git(w.Local, "switch", "-q", "--detach", "main")
	w.commit(w.Local, "", "only-here.txt", "only here")
	_, _, err := run(t, "create", "synthetic-refused", "--parent", "main", "--apply")
	if err == nil || !strings.Contains(err.Error(), "would leave them behind") || !strings.Contains(err.Error(), "git branch <name> HEAD") {
		t.Errorf("detached work only HEAD reaches = %v, want the refusal naming how to keep it", err)
	}
}

package cli_test

import (
	"strings"
	"testing"
)

// --scope stack is the path to the trunk and everything above the target, so
// a sibling of the target is outside it. Restacking the shared parent left the
// sibling on that parent's old commits, under a report that everything now
// matched. A narrower scope is a choice; what was missing was being told, and
// the one command that brings the sibling along.
func TestJourneyARestackSaysWhichBranchesItLeavesOnTheOldCommits(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--parent", "main", "--apply")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	mustRun(t, "track", "--parent", "synthetic-a", "--apply")
	w.branchOff("synthetic-a", "synthetic-sibling", "sibling.txt")
	mustRun(t, "track", "--parent", "synthetic-a", "--apply")
	w.commit(w.Local, "main", "trunk.txt", "the trunk moved")
	w.git(w.Local, "switch", "-q", "synthetic-b")

	const bringsAlong = "g2g restack --branch synthetic-a"
	preview := mustRun(t, "restack", "--scope", "stack")
	if !strings.Contains(preview, "Leaves synthetic-sibling, outside this selection, on the commits synthetic-a had before this replay") || !strings.Contains(preview, bringsAlong) {
		t.Errorf("the preview does not say what it leaves behind:\n%s", preview)
	}
	applied := mustRun(t, "restack", "--scope", "stack", "--apply")
	if !strings.Contains(applied, "Suggested next step: "+bringsAlong) {
		t.Errorf("the apply does not suggest bringing the sibling along:\n%s", applied)
	}
	w.assertClean(w.Local)
	if w.contains(w.Local, "synthetic-a", "synthetic-sibling") {
		t.Fatal("the sibling moved, so this no longer shows what --scope stack leaves")
	}

	mustRun(t, strings.Fields(bringsAlong)[1:]...)
	mustRun(t, append(strings.Fields(bringsAlong)[1:], "--apply")...)
	w.assertClean(w.Local)
	if !w.contains(w.Local, "synthetic-a", "synthetic-sibling") {
		t.Error("the suggested restack did not bring the sibling onto synthetic-a")
	}
	w.assertHas(w.Local, "synthetic-sibling", "sibling.txt")
	if got := w.git(w.Local, "rev-list", "--count", "synthetic-a..synthetic-sibling"); got != "1" {
		t.Errorf("the sibling carries %s commits of its own after the replay, want 1", got)
	}
}

// Each branch left behind is named with the replay it hangs from, and one
// restack per replay brings those along.
func TestJourneyARestackNamesWhatItLeavesAtEachHeight(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--parent", "main", "--apply")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	mustRun(t, "track", "--parent", "synthetic-a", "--apply")
	w.branchOff("synthetic-b", "synthetic-c", "c.txt")
	mustRun(t, "track", "--parent", "synthetic-b", "--apply")
	w.branchOff("synthetic-a", "synthetic-beside-b", "beside-b.txt")
	mustRun(t, "track", "--parent", "synthetic-a", "--apply")
	w.branchOff("synthetic-b", "synthetic-beside-c", "beside-c.txt")
	mustRun(t, "track", "--parent", "synthetic-b", "--apply")
	w.commit(w.Local, "main", "trunk.txt", "the trunk moved")
	w.git(w.Local, "switch", "-q", "synthetic-c")

	preview := mustRun(t, "restack", "--scope", "stack")
	want := "Leaves synthetic-beside-b and synthetic-beside-c, outside this selection, on the commits synthetic-a and synthetic-b had before this replay"
	if !strings.Contains(preview, want) || !strings.Contains(preview, "g2g restack --branch synthetic-a") {
		t.Errorf("the preview does not group what it leaves under its replay:\n%s", preview)
	}
}

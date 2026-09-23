package cli_test

import (
	"strings"
	"testing"
)

// two-trunks: a second trunk has to be said out loud before anything can be
// started on it, and once it has, a stack on it never reaches the other.
func TestJourneyTwoTrunks(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-staging", "staging.txt")
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")

	if _, _, err := run(t, "create", "synthetic-s1", "--parent", "synthetic-staging", "--apply"); err == nil {
		t.Fatal("create on a branch nobody recorded succeeded, making a feature branch a trunk by accident")
	}
	mustRun(t, "track", "--branch", "synthetic-staging", "--as-trunk", "--apply")
	mustRun(t, "create", "synthetic-s1", "--parent", "synthetic-staging", "--apply")
	w.commit(w.Local, "synthetic-s1", "s1.txt", "s1")
	w.assertStructure(map[string]string{"synthetic-s1": "synthetic-staging", "synthetic-a": "main"})

	staging := w.tip(w.Local, "synthetic-staging")
	w.commit(w.Local, "main", "moved.txt", "main moved")
	mustRun(t, "restack", "--branch", "synthetic-s1", "--apply")
	if w.tip(w.Local, "synthetic-staging") != staging || w.contains(w.Local, "main", "synthetic-s1") {
		t.Error("restacking a stack on staging reached main")
	}
	w.assertClean(w.Local)
}

// landing-branch, as far as the stacks above the declared trunk: they land on
// it, it is never replayed onto what it lands into, and nothing that reads
// ancestry puts it back in the stack below.
func TestJourneyADeclaredTrunkBoundsTheStackAboveIt(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-feature", "feature.txt")
	w.branchOff("synthetic-feature", "synthetic-a1", "a1.txt")
	w.branchOff("synthetic-a1", "synthetic-a2", "a2.txt")
	mustRun(t, "adopt", "--trunk", "main", "--apply")

	out := mustRun(t, "track", "--branch", "synthetic-feature", "--as-trunk", "--into", "main", "--by", "rebase", "--apply")
	if !strings.Contains(out, "lands into main by rebase") || !strings.Contains(out, "Removes its recorded parent, main") {
		t.Errorf("the declaration did not say what it records and what it removes:\n%s", out)
	}
	w.assertStructure(map[string]string{"synthetic-feature": "", "synthetic-a1": "synthetic-feature"})
	if out := mustRun(t, "status", "--branch", "synthetic-a2"); !strings.Contains(out, "synthetic-feature is a trunk that lands into main by rebase") {
		t.Errorf("status does not say where the trunk lands:\n%s", out)
	}

	before := w.readStore()
	if out, _, err := run(t, "adopt", "--branch", "synthetic-a2", "--trunk", "main", "--apply"); err == nil {
		t.Errorf("adopt down to main put the declared trunk back in the stack below it:\n%s", out)
	}
	if w.readStore() != before {
		t.Error("a refused adoption changed the graph")
	}

	// main moves on and the stack is restacked: the declared trunk is its base,
	// so it stays exactly where it is and nothing above it reaches main.
	feature := w.tip(w.Local, "synthetic-feature")
	w.commit(w.Local, "main", "moved.txt", "main moved")
	mustRun(t, "restack", "--branch", "synthetic-a2", "--scope", "stack", "--apply")
	if w.tip(w.Local, "synthetic-feature") != feature {
		t.Error("a restack replayed the declared trunk")
	}
	if w.contains(w.Local, "main", "synthetic-a2") {
		t.Error("the stack above the declared trunk was replayed onto main")
	}

	// A branch landing into feature moves it on, and the stack above follows it.
	w.commit(w.Local, "synthetic-feature", "landed.txt", "landed into feature")
	mustRun(t, "restack", "--branch", "synthetic-a2", "--scope", "stack", "--apply")
	if !w.contains(w.Local, "synthetic-feature", "synthetic-a2") {
		t.Error("the stack was not replayed onto the trunk it sits on")
	}
	w.assertHas(w.Local, "synthetic-a2", "a1.txt")
	w.assertClean(w.Local)
}

// track --parent names the branch, so it is the way back, and it says so.
func TestJourneyTrackingADeclaredTrunkPutsItBackInTheStack(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-feature", "feature.txt")
	w.branchOff("synthetic-feature", "synthetic-a1", "a1.txt")
	mustRun(t, "adopt", "--trunk", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-feature", "--as-trunk", "--into", "main", "--by", "merge", "--apply")

	out := mustRun(t, "track", "--branch", "synthetic-feature", "--parent", "main", "--apply")
	if !strings.Contains(out, "stops being a trunk that lands into main by merge") {
		t.Errorf("track did not say the declaration goes:\n%s", out)
	}
	w.assertStructure(map[string]string{"synthetic-feature": "main", "synthetic-a1": "synthetic-feature"})
	if strings.Contains(w.readStore(), `"declared"`) {
		t.Errorf("the declaration survived:\n%s", w.readStore())
	}
	w.assertClean(w.Local)
}

// untrack ends a declaration and strands what sat on it, and doctor names the
// way back for the stranded branch.
func TestJourneyUntrackingADeclaredTrunk(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-feature", "feature.txt")
	w.branchOff("synthetic-feature", "synthetic-a1", "a1.txt")
	mustRun(t, "track", "--branch", "synthetic-feature", "--as-trunk", "--apply")
	mustRun(t, "track", "--branch", "synthetic-a1", "--parent", "synthetic-feature", "--apply")

	out := mustRun(t, "untrack", "--branch", "synthetic-feature", "--apply")
	if !strings.Contains(out, "synthetic-feature stops being a trunk") {
		t.Errorf("untrack did not say the trunk goes:\n%s", out)
	}
	if got := doctorNames(t, "synthetic-a1"); strings.Join(got, " ") != "track --branch synthetic-a1" {
		t.Errorf("doctor names %v for the stranded branch", got)
	}
	w.assertClean(w.Local)
}

// A declared trunk that lands somewhere is a stack of one to land and push, and
// still a trunk to walk from: up steps onto what sits on it.
func TestJourneyUpFromADeclaredTrunkStepsOntoItsStack(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-feature", "feature.txt")
	w.branchOff("synthetic-feature", "synthetic-a1", "a1.txt")
	mustRun(t, "adopt", "--trunk", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-feature", "--as-trunk", "--into", "main", "--by", "merge", "--apply")
	w.git(w.Local, "switch", "-q", "synthetic-feature")

	mustRun(t, "up")
	if on := w.git(w.Local, "branch", "--show-current"); on != "synthetic-a1" {
		t.Errorf("up from the declared trunk went to %q, want synthetic-a1", on)
	}
	w.assertClean(w.Local)
}

package cli_test

import (
	"strings"
	"testing"
)

// A prune forgets every landed branch that strands nothing, and keeps the
// ones that would, naming what lets them go. One stack whose child had not
// been replayed yet used to refuse the prune of every other stack on the
// trunk, however many of them had landed.
func TestJourneyPruneForgetsWhatStrandsNothingAndKeepsTheRest(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-held", "held.txt")
	w.branchOff("synthetic-held", "synthetic-held-top", "held-top.txt")
	w.branchOff("main", "synthetic-done", "done.txt")
	w.branchOff("main", "synthetic-open", "open.txt")
	for branch, parent := range map[string]string{"synthetic-held": "main", "synthetic-held-top": "synthetic-held", "synthetic-done": "main", "synthetic-open": "main"} {
		mustRun(t, "track", "--branch", branch, "--parent", parent, "--apply")
	}
	w.git(w.Local, "push", "-q", "origin", "synthetic-held", "synthetic-held-top", "synthetic-done", "synthetic-open")
	w.git(w.Other, "fetch", "-q", "origin")
	for _, landed := range []string{"synthetic-held", "synthetic-done"} {
		w.git(w.Other, "merge", "-q", "--squash", "origin/"+landed)
		w.git(w.Other, "commit", "-qm", "synthetic squash of "+landed)
	}
	w.git(w.Other, "push", "-q", "origin", "main")
	w.git(w.Local, "switch", "-q", "main")
	mustRun(t, "pull", "--trunk-only", "--apply")

	preview := mustRun(t, "prune", "--scope", "trunk")
	for _, want := range []string{
		"Forgets synthetic-done from the recorded graph.",
		"The rest stay recorded: forgetting synthetic-held would strand branches recorded under them.",
		"g2g track --branch synthetic-held-top --parent main",
	} {
		if !strings.Contains(preview, want) {
			t.Errorf("preview lacks %q:\n%s", want, preview)
		}
	}
	if strings.Contains(preview, "Apply blocked") {
		t.Errorf("a prune with something safe to forget is blocked:\n%s", preview)
	}

	mustRun(t, "prune", "--scope", "trunk", "--apply")
	structure := w.readStructure()
	if _, recorded := structure["synthetic-done"]; recorded {
		t.Errorf("synthetic-done is still recorded: %v", structure)
	}
	if structure["synthetic-held"] != "main" || structure["synthetic-held-top"] != "synthetic-held" || structure["synthetic-open"] != "main" {
		t.Errorf("recorded structure = %v, want the held stack and the open branch as they were", structure)
	}

	// The stack that was held back is put right on its own, and then goes.
	mustRun(t, "pull", "--branch", "synthetic-held-top", "--prune", "--apply")
	w.assertClean(w.Local)
	if structure := w.readStructure(); structure["synthetic-held-top"] != "main" || structure["synthetic-held"] != "" {
		t.Errorf("recorded structure = %v, want synthetic-held forgotten and its child on main", structure)
	}
}

// With nothing safe to forget there is no prune to run, and it is the refusal
// it always was.
func TestJourneyPruneWithOnlyStrandingBranchesStillRefuses(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-held", "held.txt")
	w.branchOff("synthetic-held", "synthetic-held-top", "held-top.txt")
	mustRun(t, "track", "--branch", "synthetic-held", "--parent", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-held-top", "--parent", "synthetic-held", "--apply")
	w.git(w.Local, "push", "-q", "origin", "synthetic-held")
	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "merge", "-q", "--squash", "origin/synthetic-held")
	w.git(w.Other, "commit", "-qm", "synthetic squash")
	w.git(w.Other, "push", "-q", "origin", "main")
	w.git(w.Local, "switch", "-q", "main")
	mustRun(t, "pull", "--trunk-only", "--apply")
	before := w.readStore()

	preview := mustRun(t, "prune", "--scope", "trunk")
	if !strings.Contains(preview, "Apply blocked") || !strings.Contains(preview, "landed · forget") {
		t.Errorf("preview does not show the landed branch and the refusal:\n%s", preview)
	}
	if _, _, err := run(t, "prune", "--scope", "trunk", "--apply"); err == nil {
		t.Error("a prune that can forget nothing was applied")
	}
	if w.readStore() != before {
		t.Error("a refused prune changed the record")
	}
}

package cli_test

import (
	"os"
	"path/filepath"
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

// landedPair is two branches on the trunk that have both landed there.
func landedPair(t *testing.T) *world {
	t.Helper()
	w := newWorld(t)
	w.branchOff("main", "synthetic-free", "free.txt")
	w.branchOff("main", "synthetic-held", "held.txt")
	mustRun(t, "track", "--branch", "synthetic-free", "--parent", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-held", "--parent", "main", "--apply")
	w.git(w.Local, "push", "-q", "origin", "synthetic-free", "synthetic-held")
	w.git(w.Other, "fetch", "-q", "origin")
	for _, landed := range []string{"synthetic-free", "synthetic-held"} {
		w.git(w.Other, "merge", "-q", "--squash", "origin/"+landed)
		w.git(w.Other, "commit", "-qm", "synthetic squash of "+landed)
	}
	w.git(w.Other, "push", "-q", "origin", "main")
	w.git(w.Local, "switch", "-q", "main")
	mustRun(t, "pull", "--trunk-only", "--apply")
	return w
}

// A cleanup deletes every landed branch it can and keeps, whole, the ones a
// worktree has checked out. Where each piece of work has a worktree of its
// own, a branch that has landed is usually still checked out in the one it
// was built in, and the first of those used to refuse the lot.
func TestJourneyPruneDeletesWhatIsFreeAndKeepsWhatAWorktreeHolds(t *testing.T) {
	w := landedPair(t)
	elsewhere := filepath.Join(t.TempDir(), "synthetic-elsewhere")
	w.git(w.Local, "worktree", "add", "-q", elsewhere, "synthetic-held")
	// Both pull requests were seen merged. The held branch has landed, so it
	// is kept for its worktree and not for want of its content in the trunk.
	observed := `{"schemaVersion":1,"branches":{` +
		`"synthetic-free":{"pullRequest":{"number":1,"headRefName":"synthetic-free","state":"MERGED"}},` +
		`"synthetic-held":{"pullRequest":{"number":2,"headRefName":"synthetic-held","state":"MERGED"}}}}`
	if err := os.WriteFile(filepath.Join(w.Local, ".git", "g2g", "pull-requests.json"), []byte(observed), 0o600); err != nil {
		t.Fatal(err)
	}

	preview := mustRun(t, "prune", "--scope", "trunk", "--delete-branches")
	if strings.Contains(preview, "last seen merged") || strings.Contains(preview, "g2g delete") {
		t.Errorf("a landed branch a worktree holds was said not to be in the trunk:\n%s", preview)
	}
	for _, want := range []string{
		"Deletes local branches synthetic-free.",
		"Kept as they are, record and branch: synthetic-held is checked out in ",
		"switch --detach",
	} {
		if !strings.Contains(preview, want) {
			t.Errorf("preview lacks %q:\n%s", want, preview)
		}
	}
	mustRun(t, "prune", "--scope", "trunk", "--delete-branches", "--apply")
	branches := w.git(w.Local, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	if strings.Contains(branches, "synthetic-free") || !strings.Contains(branches, "synthetic-held") {
		t.Errorf("branches after the cleanup:\n%s", branches)
	}
	structure := w.readStructure()
	if _, recorded := structure["synthetic-free"]; recorded || structure["synthetic-held"] != "main" {
		t.Errorf("recorded structure = %v, want only the held branch still recorded", structure)
	}

	// With the worktree gone, the branch that was kept goes too.
	w.git(w.Local, "worktree", "remove", elsewhere)
	mustRun(t, "prune", "--scope", "trunk", "--delete-branches", "--apply")
	if strings.Contains(w.git(w.Local, "for-each-ref", "--format=%(refname:short)", "refs/heads"), "synthetic-held") {
		t.Error("synthetic-held survived a cleanup once nothing held it")
	}
}

// With every landed branch held there is nothing to clean up, and it is the
// refusal it was, naming each one.
func TestJourneyPruneWithEveryLandedBranchHeldStillRefuses(t *testing.T) {
	w := landedPair(t)
	for _, branch := range []string{"synthetic-free", "synthetic-held"} {
		w.git(w.Local, "worktree", "add", "-q", filepath.Join(t.TempDir(), branch), branch)
	}
	before := w.readStore()
	preview := mustRun(t, "prune", "--scope", "trunk", "--delete-branches")
	for _, want := range []string{"Apply blocked", "synthetic-free is checked out in ", "synthetic-held is checked out in "} {
		if !strings.Contains(preview, want) {
			t.Errorf("preview lacks %q:\n%s", want, preview)
		}
	}
	if _, _, err := run(t, "prune", "--scope", "trunk", "--delete-branches", "--apply"); err == nil {
		t.Error("a cleanup with nothing free to delete was applied")
	}
	if w.readStore() != before {
		t.Error("a refused cleanup changed the record")
	}
}

// A whole stack that has landed, with its top branch checked out in a
// worktree: the branch below is kept so as not to strand it, and the reason
// given is the worktree. It used to read as a branch that would be stranded,
// with untrack offered -- which forgets a branch prune could then never
// delete.
func TestJourneyPruneSaysAWorktreeHoldsTheStackItCannotCleanUp(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-low", "low.txt")
	w.branchOff("synthetic-low", "synthetic-top", "top.txt")
	mustRun(t, "track", "--branch", "synthetic-low", "--parent", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-top", "--parent", "synthetic-low", "--apply")
	w.git(w.Local, "push", "-q", "origin", "synthetic-low", "synthetic-top")
	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "merge", "-q", "--squash", "origin/synthetic-top")
	w.git(w.Other, "commit", "-qm", "synthetic squash of the stack")
	w.git(w.Other, "push", "-q", "origin", "main")
	w.git(w.Local, "switch", "-q", "main")
	mustRun(t, "pull", "--trunk-only", "--apply")
	elsewhere := filepath.Join(t.TempDir(), "synthetic-elsewhere")
	w.git(w.Local, "worktree", "add", "-q", elsewhere, "synthetic-top")

	preview := mustRun(t, "prune", "--scope", "trunk", "--delete-branches")
	for _, want := range []string{"Apply blocked", "synthetic-top is checked out in ", "switch --detach"} {
		if !strings.Contains(preview, want) {
			t.Errorf("preview lacks %q:\n%s", want, preview)
		}
	}
	for _, wrong := range []string{"would strand", "g2g untrack"} {
		if strings.Contains(preview, wrong) {
			t.Errorf("preview says %q of a branch a worktree holds:\n%s", wrong, preview)
		}
	}

	// Let go of there, the whole stack is cleaned up.
	w.git(elsewhere, "switch", "-q", "--detach")
	mustRun(t, "prune", "--scope", "trunk", "--delete-branches", "--apply")
	if left := w.git(w.Local, "for-each-ref", "--format=%(refname:short)", "refs/heads"); left != "main" {
		t.Errorf("branches after the cleanup: %q, want only main", left)
	}
}

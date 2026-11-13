package cli_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// A worktree made at the trunk's remote copy -- git worktree add <path>
// origin/main, the usual way to start fresh work -- is detached at a commit
// the local trunk has not caught up to. create refused there without saying
// how to start at that commit, and with --parent main started at the local
// trunk's older tip, moving the checkout back without saying so.
func TestJourneyCreateFromAWorktreeAtTheTrunksRemoteCopy(t *testing.T) {
	w := newWorld(t)
	w.commit(w.Other, "main", "upstream.txt", "a colleague's change")
	w.git(w.Other, "push", "-q", "origin", "main")
	w.git(w.Local, "fetch", "-q", "origin")
	fresh := filepath.Join(filepath.Dir(w.Local), "fresh")
	w.git(w.Local, "worktree", "add", "-q", fresh, "origin/main")
	t.Chdir(fresh)
	head := w.tip(fresh, "HEAD")

	refused, _, _ := run(t, "create", "synthetic-x")
	if !strings.Contains(refused, "g2g create synthetic-x --parent main --at HEAD") {
		t.Errorf("the detached refusal does not offer starting at HEAD:\n%s", refused)
	}

	behind, _, err := run(t, "create", "synthetic-x", "--parent", "main")
	if err != nil {
		t.Fatalf("create --parent main: %v\n%s", err, behind)
	}
	for _, want := range []string{"HEAD is 1 commit ahead of main", "g2g create synthetic-x --parent main --at HEAD"} {
		if !strings.Contains(behind, want) {
			t.Errorf("the preview starting at main's older tip does not say %q:\n%s", want, behind)
		}
	}

	mustRun(t, "create", "synthetic-x", "--parent", "main", "--at", "HEAD", "--apply")
	if got := w.tip(fresh, "synthetic-x"); got != head {
		t.Errorf("synthetic-x starts at %s, want HEAD %s", got, head)
	}
	if got := w.readStructure()["synthetic-x"]; got != "main" {
		t.Errorf("synthetic-x is recorded under %q, want main", got)
	}
	w.assertClean(fresh)
}

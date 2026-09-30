package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/cli"
)

// A bottom-branch conflict must retain the entire three-branch selection, even
// from a linked worktree. The base advances; both children still need replay.
func TestJourneyPullConflictAtTheBottomKeepsTheWholeStackResumable(t *testing.T) {
	for _, linked := range []bool{false, true} {
		t.Run(map[bool]string{false: "main-worktree", true: "linked-worktree"}[linked], func(t *testing.T) {
			w := newWorld(t)
			w.branchOff("main", "synthetic-a", "base.txt")
			w.branchOff("synthetic-a", "synthetic-b", "b.txt")
			w.branchOff("synthetic-b", "synthetic-c", "c.txt")
			for _, pair := range [][2]string{{"synthetic-a", "main"}, {"synthetic-b", "synthetic-a"}, {"synthetic-c", "synthetic-b"}} {
				mustRun(t, "track", "--branch", pair[0], "--parent", pair[1], "--apply")
			}
			checkout := w.Local
			if linked {
				checkout = filepath.Join(t.TempDir(), "synthetic-linked")
				w.git(w.Local, "switch", "-q", "--detach", "main")
				w.git(w.Local, "worktree", "add", "-q", checkout, "synthetic-c")
				t.Chdir(checkout)
			}
			w.commit(w.Other, "main", "base.txt", "synthetic upstream")
			w.git(w.Other, "push", "-q", "origin", "main")
			out, _, err := run(t, "pull", "--apply")
			if !cli.StoppedPartWayForTest(err) {
				t.Fatalf("pull error = %v, want status 3:\n%s", err, out)
			}
			common := w.git(checkout, "rev-parse", "--path-format=absolute", "--git-common-dir")
			journal := filepath.Join(common, "g2g", "restack.json")
			if _, err := os.Stat(journal); err != nil {
				t.Fatalf("stopped pull lost journal: %v", err)
			}
			if got := w.tip(w.Local, "main"); got != w.tip(w.Remote, "main") {
				t.Fatal("pull did not advance base")
			}
			if err := os.WriteFile(filepath.Join(checkout, "base.txt"), []byte("synthetic resolved\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			w.git(checkout, "add", "base.txt")
			mustRun(t, "restack", "--continue")
			w.assertClean(checkout)
			w.assertClean(w.Local)
			for _, pair := range [][2]string{{"main", "synthetic-a"}, {"synthetic-a", "synthetic-b"}, {"synthetic-b", "synthetic-c"}} {
				if !w.contains(w.Local, pair[0], pair[1]) {
					t.Errorf("%s was not replayed onto %s", pair[1], pair[0])
				}
				if own := strings.Fields(w.git(w.Local, "rev-list", pair[0]+".."+pair[1])); len(own) != 1 {
					t.Errorf("%s has %d own commits, want 1", pair[1], len(own))
				}
			}
			if _, err := os.Stat(journal); !os.IsNotExist(err) {
				t.Fatalf("completed pull kept journal: %v", err)
			}
		})
	}
}

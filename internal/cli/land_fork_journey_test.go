package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/cli"
)

// Landing the bottom branch of a stack that forks above it, where replaying
// both lines onto the merge conflicts. land used to refuse before merging,
// because a conflicting rewrite of a fork was refused, and the way it offered
// -- pull each line first -- rewrote the approved pull request before it
// merged. It merges as approved, stops on the first conflict as a straight
// stack does, and the restack it stopped in carries on to the other line.
func TestJourneyLandMergesBelowAForkWhoseReplayConflicts(t *testing.T) {
	w := newWorld(t)
	w.commit(w.Local, "main", "b.txt", "base")
	w.commit(w.Local, "main", "c.txt", "base")
	w.git(w.Local, "push", "-q", "origin", "main")
	w.branchOff("main", "synthetic-a", "a.txt")
	w.branchOff("synthetic-a", "synthetic-b", "b-own.txt")
	w.commit(w.Local, "synthetic-b", "b.txt", "from b")
	w.branchOff("synthetic-a", "synthetic-c", "c-own.txt")
	w.commit(w.Local, "synthetic-c", "c.txt", "from c")
	for _, edge := range [][2]string{{"synthetic-a", "main"}, {"synthetic-b", "synthetic-a"}, {"synthetic-c", "synthetic-a"}} {
		mustRun(t, "track", "--branch", edge[0], "--parent", edge[1], "--apply")
	}
	w.git(w.Local, "push", "-q", "origin", "synthetic-a", "synthetic-b", "synthetic-c")
	w.commit(w.Local, "main", "b.txt", "from the trunk")
	w.commit(w.Local, "main", "c.txt", "from the trunk")
	w.git(w.Local, "push", "-q", "origin", "main")
	w.git(w.Local, "switch", "-q", "synthetic-a")
	approved := w.tip(w.Remote, "synthetic-a")
	state := landingGitHubPulls(t, w.Remote, [2]string{"synthetic-a", "main"}, [2]string{"synthetic-b", "synthetic-a"}, [2]string{"synthetic-c", "synthetic-a"})

	if preview := mustRun(t, "land", "--branch", "synthetic-a", "--scope", "path"); strings.Contains(preview, "Apply blocked") {
		t.Fatalf("land refused a fork whose replay conflicts:\n%s", preview)
	}
	stdout, _, err := run(t, "land", "--branch", "synthetic-a", "--scope", "path", "--apply")
	if !cli.StoppedPartWayForTest(err) {
		t.Fatalf("land error = %v, want it stopped part-way on the conflict:\n%s", err, stdout)
	}
	for _, want := range []string{"stopped on a conflict in b.txt", "g2g restack --continue", "rerun g2g land"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the stop does not say %q:\n%s", want, stdout)
		}
	}
	if got := readState(t, state, "pr-41.state"); got != "MERGED" {
		t.Fatalf("pull request #41 state = %q, want MERGED", got)
	}
	// Merged as approved: nothing rewrote the pull request's head first.
	if published := w.tip(w.Remote, "synthetic-a"); published != approved {
		t.Errorf("synthetic-a was republished as %s before merging, want the approved %s", published[:8], approved[:8])
	}
	for stops := 0; ; stops++ {
		if stops == 3 {
			t.Fatal("the restack kept stopping")
		}
		for _, name := range strings.Fields(w.git(w.Local, "diff", "--name-only", "--diff-filter=U")) {
			if err := os.WriteFile(filepath.Join(w.Local, name), []byte("resolved\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			w.git(w.Local, "add", name)
		}
		if _, _, err := run(t, "restack", "--continue"); err == nil {
			break
		}
	}
	w.assertClean(w.Local)

	mustRun(t, "land", "--branch", "synthetic-a", "--scope", "path", "--apply")
	w.assertClean(w.Local)
	if w.git(w.Local, "branch", "--list", "synthetic-a") != "" || w.git(w.Remote, "branch", "--list", "synthetic-a") != "" {
		t.Error("synthetic-a is still there after the second land")
	}
	trunk := w.tip(w.Remote, "main")
	for _, branch := range []string{"synthetic-b", "synthetic-c"} {
		if local, remote := w.tip(w.Local, branch), w.tip(w.Remote, branch); local != remote {
			t.Errorf("%s is %s here and %s on the remote", branch, local[:8], remote[:8])
		}
		if !w.contains(w.Remote, trunk, branch) {
			t.Errorf("%s on the remote is not built on the trunk the merge produced", branch)
		}
	}
	if status := mustRun(t, "status"); strings.Contains(status, "synthetic-a") {
		t.Errorf("status still records synthetic-a:\n%s", status)
	}
}

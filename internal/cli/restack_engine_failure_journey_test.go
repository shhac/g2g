package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/cli"
)

// rebaseObstacle is something that stops git rebase from starting without
// being a conflict, and what Git says about it.
type rebaseObstacle struct {
	name string
	// place puts the obstacle in the way and returns what removes it.
	place func(t *testing.T, gitDir string) (remove func())
	says  string
}

// A git directory nothing may be created in is what a filesystem sandbox
// leaves, and it is the case Git reports as success: it cannot make its
// rebase-merge directory, says so, and exits zero having moved nothing. A stale
// index lock is the same failure with an honest exit status.
func rebaseObstacles() []rebaseObstacle {
	return []rebaseObstacle{
		{
			name: "git-directory-not-writable",
			place: func(t *testing.T, gitDir string) func() {
				t.Helper()
				if os.Geteuid() == 0 {
					t.Skip("a read-only directory does not stop root")
				}
				writable := func() {
					if err := os.Chmod(gitDir, 0o755); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Chmod(gitDir, 0o555); err != nil {
					t.Fatal(err)
				}
				// Whatever the test does, the temporary directory has to be
				// removable afterwards.
				t.Cleanup(writable)
				return writable
			},
			says: "rebase-merge",
		},
		{
			name: "index-locked",
			place: func(t *testing.T, gitDir string) func() {
				t.Helper()
				lock := filepath.Join(gitDir, "index.lock")
				if err := os.WriteFile(lock, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				return func() {
					if err := os.Remove(lock); err != nil {
						t.Fatal(err)
					}
				}
			},
			says: "index.lock",
		},
	}
}

// conflictingWorld is one branch, checked out, whose replay onto the trunk
// conflicts, so applying it goes through the rebase engine.
func conflictingWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "base.txt")
	w.commit(w.Local, "synthetic-a", "a.txt", "synthetic second")
	w.commit(w.Local, "synthetic-a", "more.txt", "synthetic third")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	return w
}

// assertNeverStarted checks a replay Git could not start reads as one: what
// Git said, that nothing moved, and no journal left refusing every command
// over a restack with nothing to continue.
func (w *world) assertNeverStarted(stdout string, err error, obstacle rebaseObstacle, tip string) {
	w.t.Helper()
	if err == nil {
		w.t.Fatalf("error = nil, want what Git said:\n%s", stdout)
	}
	said := stdout + err.Error()
	for _, want := range []string{obstacle.says, "nothing was replayed"} {
		if !strings.Contains(said, want) {
			w.t.Errorf("missing %q:\n%s", want, said)
		}
	}
	for _, unwanted := range []string{"did not settle", "nothing left unmerged", "restack --abort"} {
		if strings.Contains(said, unwanted) {
			w.t.Errorf("reads as a restack left to finish (%q):\n%s", unwanted, said)
		}
	}
	if got := w.tip(w.Local, "synthetic-a"); got != tip {
		w.t.Errorf("synthetic-a = %s, want %s, where it was", got, tip)
	}
	if got := w.git(w.Local, "branch", "--show-current"); got != "synthetic-a" {
		w.t.Errorf("checkout is on %q, want synthetic-a", got)
	}
	if _, statErr := os.Stat(filepath.Join(w.Local, ".git", "g2g", "restack.json")); !os.IsNotExist(statErr) {
		w.t.Errorf("a replay that never started left a journal: %v", statErr)
	}
}

// assertReplaysOnceClear checks the same apply works once the obstacle is
// gone: it stops on the conflict the preview advertised, and finishes.
func (w *world) assertReplaysOnceClear() {
	w.t.Helper()
	w.assertClean(w.Local)
	stdout, _, err := run(w.t, "restack", "--branch", "synthetic-a", "--apply")
	if !cli.StoppedPartWayForTest(err) || !strings.Contains(stdout, "Stopped on a conflict in base.txt") {
		w.t.Fatalf("restack error = %v, want a stop on the advertised conflict:\n%s", err, stdout)
	}
	w.resolveRestack(w.Local)
	w.assertClean(w.Local)
	if !w.contains(w.Local, "main", "synthetic-a") {
		w.t.Error("synthetic-a was not replayed onto main")
	}
}

// A rebase Git could not start is not a restack stopped part-way. Git exits
// zero from one of these having done nothing, so the restack planned the same
// rebase again, gave up counting passes, and left a journal under advice to
// continue a replay that would fail the same way.
func TestJourneyRestackSaysWhyTheRebaseCouldNotStart(t *testing.T) {
	for _, obstacle := range rebaseObstacles() {
		t.Run(obstacle.name, func(t *testing.T) {
			w := conflictingWorld(t)
			w.commit(w.Local, "main", "base.txt", "synthetic trunk")
			w.git(w.Local, "switch", "-q", "synthetic-a")
			tip := w.tip(w.Local, "synthetic-a")

			remove := obstacle.place(t, filepath.Join(w.Local, ".git"))
			stdout, _, err := run(t, "restack", "--branch", "synthetic-a", "--onto", "main", "--apply")
			remove()

			if cli.StoppedPartWayForTest(err) {
				t.Errorf("error = %v, want a failure: nothing was done part-way:\n%s", err, stdout)
			}
			w.assertNeverStarted(stdout, err, obstacle, tip)
			w.assertReplaysOnceClear()
		})
	}
}

// pull replays through the same service, so it says the same thing. It has
// stopped part-way all the same: the trunk it advanced stays advanced, and the
// replay it could not start leaves nothing to resume.
func TestJourneyPullSaysWhyTheRebaseCouldNotStart(t *testing.T) {
	for _, obstacle := range rebaseObstacles() {
		t.Run(obstacle.name, func(t *testing.T) {
			w := conflictingWorld(t)
			w.commit(w.Other, "main", "base.txt", "synthetic upstream")
			w.git(w.Other, "push", "-q", "origin", "main")
			tip := w.tip(w.Local, "synthetic-a")

			remove := obstacle.place(t, filepath.Join(w.Local, ".git"))
			stdout, _, err := run(t, "pull", "--apply")
			remove()

			if !cli.StoppedPartWayForTest(err) {
				t.Errorf("error = %v, want status 3: the trunk had already moved:\n%s", err, stdout)
			}
			w.assertNeverStarted(stdout, err, obstacle, tip)
			if got := w.tip(w.Local, "main"); got != w.tip(w.Remote, "main") {
				t.Errorf("main = %s, want the remote's tip: the pull had advanced it before the replay", got)
			}
			w.assertReplaysOnceClear()
		})
	}
}

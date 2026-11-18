package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	localgit "github.com/shhac/g2g/internal/git"
)

// A branch created in a linked worktree and not committed to yet, while
// another checkout lands the bottom of its stack and replays its parent. The
// pull leaves it where its worktree has it, on the tip its parent used to
// have -- and the prune after it read a branch with nothing of its own as
// landed and forgot it. That was the only record of where its work begins.
// The commit made on it afterwards sat on the parent's old commits, and
// recording it again measured from where it meets the rewritten parent: a
// commit on the trunk, below all of them. The next restack offered the
// parent's old commits as the branch's own and did not apply.
//
// It is left under both policies: follow leaves a worktree with changes, and
// skip leaves a clean one too.
func TestJourneyPruneKeepsAnUnstartedBranchItsParentWasReplayedFromUnder(t *testing.T) {
	for _, held := range []struct {
		policy string
		dirty  bool
	}{{"follow", true}, {"skip", false}} {
		t.Run(held.policy, func(t *testing.T) {
			w := newWorld(t)
			w.branchOff("main", "synthetic-a", "a.txt")
			w.commit(w.Local, "synthetic-a", "a-more.txt", "a-more")
			w.branchOff("synthetic-a", "synthetic-b", "b.txt")
			w.commit(w.Local, "synthetic-b", "b-more.txt", "b-more")
			mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
			mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a", "--apply")
			w.git(w.Local, "push", "-q", "origin", "synthetic-a", "synthetic-b")
			w.git(w.Local, "switch", "-q", "main")
			agent := filepath.Join(filepath.Dir(w.Local), "agent")
			w.git(w.Local, "worktree", "add", "-q", "--detach", agent, "synthetic-b")
			t.Chdir(agent)
			mustRun(t, "create", "synthetic-c", "--parent", "synthetic-b", "--apply")
			started := w.tip(agent, "synthetic-b")
			if held.dirty {
				if err := os.WriteFile(filepath.Join(agent, "c.txt"), []byte("half done\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			// Somebody squash-merges synthetic-a, and the main checkout pulls.
			w.git(w.Other, "fetch", "-q", "origin")
			w.git(w.Other, "merge", "-q", "--squash", "origin/synthetic-a")
			w.git(w.Other, "commit", "-qm", "synthetic squash of a")
			w.git(w.Other, "push", "-q", "origin", "main")
			t.Chdir(w.Local)
			mustRun(t, "pull", "--branch", "synthetic-b", "--prune", "--worktrees", held.policy, "--apply")
			w.assertClean(w.Local)
			if !w.contains(w.Local, "main", "synthetic-b") || w.tip(w.Local, "synthetic-b") == started {
				t.Fatal("synthetic-b was not replayed onto the squashed trunk")
			}
			if w.tip(w.Local, "synthetic-c") != started {
				t.Fatal("synthetic-c was moved under its worktree")
			}
			if parent, fork := w.recordedEdge("synthetic-c"); parent != "synthetic-b" || fork != started {
				t.Fatalf("synthetic-c is recorded under %q from %q, want synthetic-b from %s, where it was created", parent, fork, started)
			}

			// The work is committed, and its own worktree restacks it.
			w.commit(agent, "synthetic-c", "c.txt", "c")
			t.Chdir(agent)
			preview := mustRun(t, "restack", "--branch", "synthetic-c", "--scope", "branch")
			if want := "Replay range for synthetic-c: " + localgit.Short(started) + ".."; !strings.Contains(preview, want) {
				t.Errorf("the preview does not replay from where synthetic-c was created (%q):\n%s", want, preview)
			}
			_, fork := w.recordedEdge("synthetic-c")
			if got := w.git(agent, "log", "--format=%s", fork+"..synthetic-c"); got != "synthetic c.txt" {
				t.Errorf("synthetic-c's own range holds %q, want only its one commit", got)
			}
			mustRun(t, "restack", "--branch", "synthetic-c", "--scope", "branch", "--apply")
			w.assertClean(agent)
			if got := w.git(agent, "log", "--format=%s", "synthetic-b..synthetic-c"); got != "synthetic c.txt" {
				t.Errorf("synthetic-c carries %q over synthetic-b after its restack, want only its one commit", got)
			}
		})
	}
}

// recordedEdge is the parent and fork point the graph records for a branch,
// both empty when it records none.
func (w *world) recordedEdge(branch string) (parent, forkPoint string) {
	w.t.Helper()
	var stored struct {
		Branches map[string]struct {
			Parent    string `json:"parent"`
			ForkPoint string `json:"forkPoint"`
		} `json:"branches"`
	}
	if err := json.Unmarshal([]byte(w.readStore()), &stored); err != nil {
		w.t.Fatalf("decode graph store: %v", err)
	}
	return stored.Branches[branch].Parent, stored.Branches[branch].ForkPoint
}

// The same unstarted branch, on the parent that lands. Asked over the range
// above the trunk, it has commits -- every one of them its parent's, and all
// of them in the squash -- so it read as landed with its parent and was
// forgotten, where one that had been started was kept. It is kept too: the
// prune says it would be stranded, and the restack its own worktree runs once
// the work is committed puts it on the trunk with that commit alone.
func TestJourneyPruneKeepsAnUnstartedBranchWhoseParentLanded(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.commit(w.Local, "synthetic-a", "a-more.txt", "a-more")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	w.git(w.Local, "push", "-q", "origin", "synthetic-a")
	w.git(w.Local, "switch", "-q", "main")
	agent := filepath.Join(filepath.Dir(w.Local), "agent")
	w.git(w.Local, "worktree", "add", "-q", "--detach", agent, "synthetic-a")
	t.Chdir(agent)
	mustRun(t, "create", "synthetic-c", "--parent", "synthetic-a", "--apply")
	started := w.tip(agent, "synthetic-a")
	if err := os.WriteFile(filepath.Join(agent, "c.txt"), []byte("half done\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "merge", "-q", "--squash", "origin/synthetic-a")
	w.git(w.Other, "commit", "-qm", "synthetic squash of a")
	w.git(w.Other, "push", "-q", "origin", "main")
	t.Chdir(w.Local)
	out, _, _ := run(t, "pull", "--branch", "synthetic-a", "--prune", "--apply")
	if !strings.Contains(out, "strand") {
		t.Errorf("the prune does not say it would strand the branch it keeps:\n%s", out)
	}
	if w.tip(w.Local, "synthetic-c") != started {
		t.Fatal("synthetic-c was moved under its worktree")
	}
	if parent, fork := w.recordedEdge("synthetic-c"); parent != "synthetic-a" || fork != started {
		t.Fatalf("synthetic-c is recorded under %q from %q, want synthetic-a from %s, where it was created", parent, fork, started)
	}

	w.commit(agent, "synthetic-c", "c.txt", "c")
	t.Chdir(agent)
	mustRun(t, "restack", "--branch", "synthetic-c", "--scope", "branch", "--apply")
	w.assertClean(agent)
	if got := w.git(agent, "log", "--format=%s", "main..synthetic-c"); got != "synthetic c.txt" {
		t.Errorf("synthetic-c carries %q over main after its restack, want only its one commit", got)
	}
	t.Chdir(w.Local)
	mustRun(t, "prune", "--branch", "synthetic-c", "--apply")
	if structure := w.readStructure(); structure["synthetic-c"] != "main" {
		t.Errorf("recorded structure = %v, want synthetic-c on main with its landed parent forgotten", structure)
	}
}

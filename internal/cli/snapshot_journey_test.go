package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// snapshotWorld is two stacks on one trunk: one branch that has landed and
// can be forgotten, and one in review that is somebody's work in progress.
func snapshotWorld(t *testing.T) (*world, string) {
	t.Helper()
	w := newWorld(t)
	w.branchOff("main", "synthetic-done", "done.txt")
	w.branchOff("main", "synthetic-open", "open.txt")
	mustRun(t, "track", "--branch", "synthetic-done", "--parent", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-open", "--parent", "main", "--apply")
	w.git(w.Local, "push", "-q", "origin", "synthetic-done", "synthetic-open")
	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "merge", "-q", "--squash", "origin/synthetic-done")
	w.git(w.Other, "commit", "-qm", "synthetic squash")
	w.git(w.Other, "push", "-q", "origin", "main")
	w.git(w.Local, "switch", "-q", "main")
	mustRun(t, "pull", "--trunk-only", "--apply")
	return w, filepath.Join(t.TempDir(), "synthetic-snapshot.json")
}

// The plan applied is the plan that was previewed. An apply given the
// snapshot carries it out without planning, and what else is going on in the
// repository does not stop it: a commit on a branch in another stack, a
// branch recorded there, the trunk moving on.
func TestJourneyASnapshotAppliesThePlanThatWasPreviewed(t *testing.T) {
	w, path := snapshotWorld(t)
	preview := mustRun(t, "prune", "--scope", "trunk", "--snapshot", path)
	if !strings.Contains(preview, "Forgets synthetic-done") || !strings.Contains(preview, "Snapshot written to "+path) {
		t.Fatalf("preview did not plan and write the snapshot:\n%s", preview)
	}
	if !strings.Contains(preview, "g2g prune --scope=trunk --snapshot "+path+" --apply") {
		t.Errorf("preview does not name the apply:\n%s", preview)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("snapshot file: %v, %v", info, err)
	}

	// The rest of the repository carries on.
	w.commit(w.Local, "synthetic-open", "more.txt", "synthetic more")
	w.branchOff("synthetic-open", "synthetic-open-top", "top.txt")
	mustRun(t, "track", "--branch", "synthetic-open-top", "--parent", "synthetic-open", "--apply")
	w.commit(w.Other, "main", "colleague.txt", "synthetic upstream")
	w.git(w.Other, "push", "-q", "origin", "main")
	w.git(w.Local, "switch", "-q", "main")
	mustRun(t, "pull", "--trunk-only", "--apply")

	applied := mustRun(t, "prune", "--scope", "trunk", "--snapshot", path, "--apply")
	if !strings.Contains(applied, "Forgets synthetic-done") || !strings.Contains(applied, "Forgotten.") {
		t.Errorf("apply did not carry out the previewed plan:\n%s", applied)
	}
	structure := w.readStructure()
	if _, recorded := structure["synthetic-done"]; recorded {
		t.Errorf("synthetic-done is still recorded: %v", structure)
	}
	if structure["synthetic-open"] != "main" || structure["synthetic-open-top"] != "synthetic-open" {
		t.Errorf("recorded structure = %v, want what was recorded since the preview kept", structure)
	}

	// Used once: what it rested on is not so any more.
	out, _, err := run(t, "prune", "--scope", "trunk", "--snapshot", path, "--apply")
	if err == nil || !strings.Contains(out+err.Error(), "what the graph records of synthetic-done changed") {
		t.Errorf("a second apply of the same snapshot = %v:\n%s", err, out)
	}
}

// What the plan rests on having changed refuses the apply, and says what.
func TestJourneyASnapshotIsRefusedWhenWhatItRestsOnChanged(t *testing.T) {
	changes := map[string]struct {
		change func(w *world)
		want   string
	}{
		"the branch gained a commit": {func(w *world) {
			w.commit(w.Local, "synthetic-done", "late.txt", "synthetic late")
			w.git(w.Local, "switch", "-q", "main")
		}, "synthetic-done moved from "},
		"a child was recorded on it": {func(w *world) {
			w.branchOff("synthetic-done", "synthetic-late", "late.txt")
			w.git(w.Local, "switch", "-q", "main")
			mustRun(t, "track", "--branch", "synthetic-late", "--parent", "synthetic-done", "--apply")
		}, "the branches recorded on synthetic-done changed"},
		"it was deleted": {func(w *world) { w.git(w.Local, "branch", "-q", "-D", "synthetic-done") }, "synthetic-done no longer exists"},
		"the trunk was rewritten": {func(w *world) {
			w.git(w.Local, "commit", "-q", "--amend", "-m", "synthetic rewritten")
		}, "main was rewritten"},
	}
	for name, each := range changes {
		t.Run(name, func(t *testing.T) {
			w, path := snapshotWorld(t)
			mustRun(t, "prune", "--scope", "trunk", "--snapshot", path)
			each.change(w)
			before := w.readStore()
			out, _, err := run(t, "prune", "--scope", "trunk", "--snapshot", path, "--apply")
			if err == nil {
				t.Fatalf("a snapshot whose basis changed was applied:\n%s", out)
			}
			if said := out + err.Error(); !strings.Contains(said, each.want) || !strings.Contains(said, "preview again") {
				t.Errorf("refusal lacks %q:\n%s", each.want, said)
			}
			if w.readStore() != before {
				t.Error("a refused snapshot changed the record")
			}
		})
	}
}

// A plan answers the request it was made for, and nothing is written when
// there is nothing to apply.
func TestJourneyASnapshotAnswersItsOwnRequestOnly(t *testing.T) {
	w, path := snapshotWorld(t)
	mustRun(t, "prune", "--scope", "trunk", "--snapshot", path)
	for _, other := range [][]string{
		{"prune", "--scope", "trunk", "--delete-branches", "--snapshot", path, "--apply"},
		{"prune", "--branch", "synthetic-open", "--snapshot", path, "--apply"},
	} {
		if out, _, err := run(t, other...); err == nil || !strings.Contains(out+err.Error(), "different request") {
			t.Errorf("%v applied a plan made for another request: %v\n%s", other, err, out)
		}
	}
	if _, recorded := w.readStructure()["synthetic-done"]; !recorded {
		t.Fatal("a plan was applied to a request it was not made for")
	}

	missing := filepath.Join(t.TempDir(), "synthetic-missing.json")
	if out, _, err := run(t, "prune", "--snapshot", missing, "--apply"); err == nil || !strings.Contains(out+err.Error(), "no snapshot at") {
		t.Errorf("applying a snapshot that is not there = %v\n%s", err, out)
	}
	nothing := filepath.Join(t.TempDir(), "synthetic-nothing.json")
	out := mustRun(t, "prune", "--branch", "synthetic-open", "--snapshot", nothing)
	if !strings.Contains(out, "No snapshot was written") {
		t.Errorf("a preview with nothing to apply did not say so:\n%s", out)
	}
	if _, err := os.Stat(nothing); !os.IsNotExist(err) {
		t.Errorf("a snapshot was written for nothing to apply: %v", err)
	}
}

// restackWorld is a two-branch stack whose trunk has moved on, so both need
// replaying.
func restackWorld(t *testing.T) (*world, string) {
	t.Helper()
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a", "--apply")
	w.commit(w.Other, "main", "colleague.txt", "synthetic upstream")
	w.git(w.Other, "push", "-q", "origin", "main")
	w.git(w.Local, "switch", "-q", "synthetic-b")
	mustRun(t, "pull", "--trunk-only", "--apply")
	return w, filepath.Join(t.TempDir(), "synthetic-restack.json")
}

// A restack previewed into a snapshot is the restack that is applied: both
// branches end on the trunk with their own commit each, the record follows,
// and the checkout is clean.
func TestJourneyARestackSnapshotAppliesTheReplayThatWasPreviewed(t *testing.T) {
	w, path := restackWorld(t)
	preview := mustRun(t, "restack", "--scope", "stack", "--snapshot", path)
	if !strings.Contains(preview, "Snapshot written to "+path) {
		t.Fatalf("preview wrote no snapshot:\n%s", preview)
	}
	tips := w.git(w.Local, "for-each-ref", "refs/heads")

	// Somebody else's branch moves; this stack does not rest on it.
	w.git(w.Local, "branch", "-q", "synthetic-elsewhere", "main")
	if w.git(w.Local, "for-each-ref", "refs/heads/synthetic-a", "refs/heads/synthetic-b") == "" || tips == "" {
		t.Fatal("the stack is missing")
	}

	mustRun(t, "restack", "--scope", "stack", "--snapshot", path, "--apply")
	w.assertClean(w.Local)
	for branch, base := range map[string]string{"synthetic-a": "main", "synthetic-b": "synthetic-a"} {
		if !w.contains(w.Local, base, branch) {
			t.Errorf("%s is not on %s after the restack", branch, base)
		}
		if own := strings.Fields(w.git(w.Local, "rev-list", base+".."+branch)); len(own) != 1 {
			t.Errorf("%s has %d commits of its own on %s, want 1", branch, len(own), base)
		}
		if _, fork := w.recordedEdge(branch); fork != w.tip(w.Local, base) {
			t.Errorf("%s is recorded as forking at %s, want %s's tip", branch, fork, base)
		}
	}
	if status := mustRun(t, "status"); strings.Contains(status, "needs restack") {
		t.Errorf("status after the restack:\n%s", status)
	}
}

func TestJourneyARestackSnapshotIsRefusedWhenWhatItRestsOnChanged(t *testing.T) {
	changes := map[string]struct {
		change func(w *world)
		want   string
	}{
		"a branch in it gained a commit": {func(w *world) {
			w.commit(w.Local, "synthetic-a", "late.txt", "synthetic late")
			w.git(w.Local, "switch", "-q", "synthetic-b")
		}, "synthetic-a moved from "},
		"the trunk moved on": {func(w *world) {
			w.commit(w.Other, "main", "again.txt", "synthetic upstream again")
			w.git(w.Other, "push", "-q", "origin", "main")
			mustRun(t, "pull", "--trunk-only", "--apply")
		}, "main moved from "},
		"the checkout is somewhere else": {func(w *world) { w.git(w.Local, "switch", "-q", "synthetic-a") }, "synthetic-a is checked out in "},
		"a child was recorded on it": {func(w *world) {
			w.git(w.Local, "branch", "-q", "synthetic-c", "synthetic-b")
			mustRun(t, "track", "--branch", "synthetic-c", "--parent", "synthetic-b", "--apply")
		}, "the branches recorded on synthetic-b changed"},
	}
	for name, each := range changes {
		t.Run(name, func(t *testing.T) {
			w, path := restackWorld(t)
			mustRun(t, "restack", "--scope", "stack", "--snapshot", path)
			each.change(w)
			tips := w.git(w.Local, "for-each-ref", "refs/heads")
			out, _, err := run(t, "restack", "--scope", "stack", "--snapshot", path, "--apply")
			if err == nil {
				t.Fatalf("a snapshot whose basis changed was applied:\n%s", out)
			}
			if said := out + err.Error(); !strings.Contains(said, each.want) {
				t.Errorf("refusal lacks %q:\n%s", each.want, said)
			}
			if w.git(w.Local, "for-each-ref", "refs/heads") != tips {
				t.Error("a refused snapshot moved a branch")
			}
		})
	}
}

// A replay that will conflict is previewed as one, and applied from the
// snapshot it stops where it would have: on the conflict, resumable.
func TestJourneyARestackSnapshotStopsOnTheConflictItPreviewed(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "shared.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	w.commit(w.Other, "main", "shared.txt", "synthetic theirs")
	w.git(w.Other, "push", "-q", "origin", "main")
	mustRun(t, "pull", "--trunk-only", "--apply")
	path := filepath.Join(t.TempDir(), "synthetic-conflict.json")
	if preview := mustRun(t, "restack", "--snapshot", path); !strings.Contains(preview, "will not apply cleanly") {
		t.Fatalf("preview did not predict the conflict:\n%s", preview)
	}
	out, _, err := run(t, "restack", "--snapshot", path, "--apply")
	if err == nil || !strings.Contains(out, "g2g restack --continue") {
		t.Fatalf("apply did not stop on the conflict: %v\n%s", err, out)
	}
	w.resolveRestack(w.Local)
	w.assertClean(w.Local)
	if !w.contains(w.Local, "main", "synthetic-a") {
		t.Error("synthetic-a is not on main after the conflict was resolved")
	}
}

// A worktree the plan brings along has to still be clean. Whether it was is
// what decided its branch would move.
func TestJourneyARestackSnapshotIsRefusedWhenAFollowedWorktreeHasChanges(t *testing.T) {
	w, path := restackWorld(t)
	w.git(w.Local, "switch", "-q", "synthetic-b")
	held := filepath.Join(t.TempDir(), "synthetic-held")
	w.git(w.Local, "switch", "-q", "main")
	w.git(w.Local, "worktree", "add", "-q", held, "synthetic-a")
	w.git(w.Local, "switch", "-q", "synthetic-b")
	mustRun(t, "restack", "--scope", "stack", "--snapshot", path)
	if err := os.WriteFile(filepath.Join(held, "a.txt"), []byte("synthetic uncommitted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tips := w.git(w.Local, "for-each-ref", "refs/heads")
	out, _, err := run(t, "restack", "--scope", "stack", "--snapshot", path, "--apply")
	if err == nil || !strings.Contains(out+err.Error(), "synthetic-a has changes in") {
		t.Fatalf("a plan that follows a worktree was applied over its changes: %v\n%s", err, out)
	}
	if w.git(w.Local, "for-each-ref", "refs/heads") != tips {
		t.Error("a refused snapshot moved a branch")
	}
}

// A branch is judged against the first ancestor that is still there, which
// with the branch below it deleted is one further down. A plan that rested
// only on the recorded parent went on calling the branch landed after that
// ancestor was moved back, and forgot the only branch carrying the work.
func TestJourneyASnapshotRestsOnTheAncestorABranchWasJudgedAgainst(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	w.branchOff("synthetic-b", "synthetic-c", "c.txt")
	for branch, parent := range map[string]string{"synthetic-a": "main", "synthetic-b": "synthetic-a", "synthetic-c": "synthetic-b"} {
		mustRun(t, "track", "--branch", branch, "--parent", parent, "--apply")
	}
	before := w.tip(w.Local, "synthetic-a")
	w.git(w.Local, "switch", "-q", "synthetic-a")
	w.git(w.Local, "merge", "-q", "--squash", "synthetic-c")
	w.git(w.Local, "commit", "-qm", "synthetic squash of the rest")
	w.git(w.Local, "branch", "-q", "-D", "synthetic-b")
	path := filepath.Join(t.TempDir(), "synthetic-ancestor.json")
	if preview := mustRun(t, "prune", "--branch", "synthetic-c", "--snapshot", path); !strings.Contains(preview, "Snapshot written") {
		t.Fatalf("preview wrote no snapshot:\n%s", preview)
	}

	w.git(w.Local, "switch", "-q", "--detach")
	w.git(w.Local, "branch", "-q", "-f", "synthetic-a", before)
	w.git(w.Local, "switch", "-q", "synthetic-a")
	out, _, err := run(t, "prune", "--branch", "synthetic-c", "--snapshot", path, "--apply")
	if err == nil || !strings.Contains(out+err.Error(), "synthetic-a moved from ") {
		t.Fatalf("a plan was applied after the branch it was judged against moved back: %v\n%s", err, out)
	}
	if _, recorded := w.readStructure()["synthetic-c"]; !recorded {
		t.Error("synthetic-c, the only branch carrying the work, was forgotten")
	}
}

// --snapshot naming something that is not a snapshot is a slip, and is
// refused before anything is planned rather than replacing it.
func TestJourneyASnapshotNeverReplacesAnotherFile(t *testing.T) {
	w, _ := snapshotWorld(t)
	notes := filepath.Join(t.TempDir(), "synthetic-notes.txt")
	if err := os.WriteFile(notes, []byte("synthetic notes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{notes, filepath.Join(w.Local, ".git", "g2g", "graph.json"), filepath.Join(t.TempDir(), "synthetic-missing", "plan.json")} {
		out, _, err := run(t, "prune", "--scope", "trunk", "--snapshot", path)
		if err == nil || strings.Contains(out, "Forgets") {
			t.Errorf("--snapshot %s planned or wrote: %v\n%s", path, err, out)
		}
	}
	if content, _ := os.ReadFile(notes); string(content) != "synthetic notes\n" {
		t.Errorf("an unrelated file was replaced: %q", content)
	}
	mustRun(t, "status")

	// One it did write is replaced by the next preview, and removed when
	// that preview has nothing to apply.
	path := filepath.Join(t.TempDir(), "synthetic-plan.json")
	mustRun(t, "prune", "--scope", "trunk", "--snapshot", path)
	mustRun(t, "prune", "--scope", "trunk", "--snapshot", path, "--apply")
	if out := mustRun(t, "prune", "--scope", "trunk", "--snapshot", path); !strings.Contains(out, "No snapshot was written") {
		t.Fatalf("second preview:\n%s", out)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("a spent snapshot was left for the next apply to read: %v", err)
	}
}

// A restack applies only to a clean working tree, so a snapshot written into
// it would refuse its own apply; and a remote's copy named by --onto is asked
// for again, since it is a commit in the plan and no branch in the basis.
func TestJourneyARestackSnapshotIsPlacedAndAimedWhereItCanApply(t *testing.T) {
	w, path := restackWorld(t)
	inside := filepath.Join(w.Local, "synthetic-plan.json")
	if out, _, err := run(t, "restack", "--scope", "stack", "--snapshot", inside); err == nil || !strings.Contains(out+err.Error(), "would be a change in the working tree") {
		t.Errorf("a snapshot inside the working tree was written: %v\n%s", err, out)
	}
	if _, err := os.Stat(inside); !os.IsNotExist(err) {
		t.Errorf("a snapshot was written into the working tree: %v", err)
	}

	w.git(w.Local, "fetch", "-q", "origin")
	mustRun(t, "restack", "--scope", "stack", "--onto", "origin/main", "--snapshot", path)
	w.commit(w.Other, "main", "again.txt", "synthetic upstream again")
	w.git(w.Other, "push", "-q", "origin", "main")
	w.git(w.Local, "fetch", "-q", "origin")
	tips := w.git(w.Local, "for-each-ref", "refs/heads")
	out, _, err := run(t, "restack", "--scope", "stack", "--onto", "origin/main", "--snapshot", path, "--apply")
	if err == nil || !strings.Contains(out+err.Error(), "origin/main is not where it was") {
		t.Errorf("a replay onto a remote's copy that moved was applied: %v\n%s", err, out)
	}
	if w.git(w.Local, "for-each-ref", "refs/heads") != tips {
		t.Error("a refused snapshot moved a branch")
	}
}

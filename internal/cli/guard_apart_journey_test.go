package cli_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// Many worktrees, each with a session on its own stack over one trunk, is the
// ordinary case once agents each have one, and a restack stopped on a conflict
// in some worktree is then the ordinary state of the repository. untrack, pull
// and prune each waited for every one of them, so sessions on stacks with
// nothing in common kept refusing one another, a different journal each time.

// stoppedElsewhere is a repository with two stacks on main and a remote whose
// main a colleague has since moved on. One stack is synthetic-busy with
// synthetic-busy-top on it, whose restack an agent's worktree has stopped
// half-way: synthetic-busy is already replayed onto main as it was then, and
// synthetic-busy-top stopped on a conflict. The other is synthetic-free with
// synthetic-free-top on it, which this checkout is working on. found is where
// the restack found the agent's two branches.
func stoppedElsewhere(t *testing.T) (w *world, agent string, found map[string]string) {
	t.Helper()
	w = newWorld(t)
	w.branchOff("main", "synthetic-busy", "busy.txt")
	mustRun(t, "track", "--parent", "main", "--apply")
	w.branchOff("synthetic-busy", "synthetic-busy-top", "shared.txt")
	mustRun(t, "track", "--parent", "synthetic-busy", "--apply")
	w.branchOff("main", "synthetic-free", "free.txt")
	mustRun(t, "track", "--parent", "main", "--apply")
	w.branchOff("synthetic-free", "synthetic-free-top", "free-top.txt")
	mustRun(t, "track", "--parent", "synthetic-free", "--apply")
	w.commit(w.Local, "main", "shared.txt", "the trunk's own version")
	w.git(w.Local, "push", "-q", "origin", "main")

	found = map[string]string{"synthetic-busy": w.tip(w.Local, "synthetic-busy"), "synthetic-busy-top": w.tip(w.Local, "synthetic-busy-top")}
	agent = filepath.Join(filepath.Dir(w.Local), "agent")
	w.git(w.Local, "worktree", "add", "-q", agent, "synthetic-busy")
	t.Chdir(agent)
	if stdout, stderr, err := run(t, "restack", "--apply"); err == nil {
		t.Fatalf("the agent's restack did not stop on its conflict:\n%s%s", stdout, stderr)
	}
	if !w.contains(agent, "main", "synthetic-busy") || w.tip(agent, "synthetic-busy-top") != found["synthetic-busy-top"] {
		t.Fatal("the agent's restack did not stop half-way, with one branch replayed and the next not")
	}
	t.Chdir(w.Local)
	w.git(w.Local, "switch", "-q", "synthetic-free-top")

	w.git(w.Other, "pull", "-q", "--ff-only")
	w.commit(w.Other, "main", "colleague.txt", "theirs")
	w.git(w.Other, "push", "-q", "origin", "main")
	return w, agent, found
}

// forkPoint is where the store records a branch as forking.
func (w *world) forkPoint(branch string) string {
	w.t.Helper()
	var stored struct {
		Branches map[string]struct {
			ForkPoint string `json:"forkPoint"`
		} `json:"branches"`
	}
	if err := json.Unmarshal([]byte(w.readStore()), &stored); err != nil {
		w.t.Fatalf("decode graph store: %v", err)
	}
	return stored.Branches[branch].ForkPoint
}

func refusedBy(t *testing.T, want string, args ...string) {
	t.Helper()
	stdout, stderr, err := run(t, args...)
	if err == nil || !strings.Contains(stdout+stderr+err.Error(), want) {
		t.Errorf("g2g %s = %v, want a refusal saying %q\n%s%s", strings.Join(args, " "), err, want, stdout, stderr)
	}
}

func TestJourneyUntrackBesideARestackStoppedElsewhere(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-busy", "busy.txt")
	mustRun(t, "track", "--parent", "main", "--apply")
	w.branchOff("synthetic-busy", "synthetic-busy-top", "contested.txt")
	mustRun(t, "track", "--parent", "synthetic-busy", "--apply")
	w.branchOff("main", "synthetic-free", "free.txt")
	mustRun(t, "track", "--parent", "main", "--apply")
	w.branchOff("synthetic-free", "synthetic-free-top", "free-top.txt")
	mustRun(t, "track", "--parent", "synthetic-free", "--apply")
	w.commit(w.Local, "synthetic-busy", "contested.txt", "a fix below the top")
	w.git(w.Local, "switch", "-q", "synthetic-free")
	recorded := w.forkPoint("synthetic-busy-top")

	// The agent replays synthetic-busy-top onto synthetic-busy: it moves the
	// one and reads the other.
	agent := filepath.Join(filepath.Dir(w.Local), "agent")
	w.git(w.Local, "worktree", "add", "-q", agent, "synthetic-busy-top")
	t.Chdir(agent)
	if stdout, stderr, err := run(t, "restack", "--apply"); err == nil {
		t.Fatalf("the agent's restack did not stop on its conflict:\n%s%s", stdout, stderr)
	}
	refusedBy(t, "already in progress", "untrack", "--branch", "synthetic-free-top", "--apply")

	t.Chdir(w.Local)
	refusedBy(t, "which holds synthetic-busy-top", "untrack", "--branch", "synthetic-busy-top", "--apply")
	refusedBy(t, "which holds synthetic-busy", "untrack", "--branch", "synthetic-busy", "--apply")
	mustRun(t, "untrack", "--branch", "synthetic-free-top", "--apply")
	if parent, tracked := w.readStructure()["synthetic-free-top"]; tracked {
		t.Errorf("synthetic-free-top is still recorded, under %q", parent)
	}
	w.assertClean(w.Local)

	// Undoing the restack puts back its own branches' records and leaves the
	// edit made beside it.
	t.Chdir(agent)
	mustRun(t, "restack", "--abort")
	structure := w.readStructure()
	if _, tracked := structure["synthetic-free-top"]; tracked {
		t.Error("aborting the agent's restack recorded synthetic-free-top again")
	}
	if structure["synthetic-busy-top"] != "synthetic-busy" || w.forkPoint("synthetic-busy-top") != recorded {
		t.Errorf("synthetic-busy-top is recorded on %q forking at %s, want synthetic-busy at %s", structure["synthetic-busy-top"], w.forkPoint("synthetic-busy-top"), recorded)
	}
	w.assertClean(agent)
}

// A pull of another stack fast-forwards the trunk the stopped restack replays
// onto. Where the trunk was stays inside it, so what the restack has already
// replayed still stands, and finishing it carries on to where the trunk is.
func TestJourneyPullBesideARestackStoppedElsewhereThatThenContinues(t *testing.T) {
	w, agent, _ := stoppedElsewhere(t)

	mustRun(t, "pull", "--apply")
	if w.tip(w.Local, "main") != w.tip(w.Remote, "main") {
		t.Error("the pull did not bring main up to date")
	}
	if !w.contains(w.Local, "main", "synthetic-free") || !w.contains(w.Local, "synthetic-free", "synthetic-free-top") {
		t.Error("the pulled stack was not replayed onto main")
	}
	w.assertHas(w.Local, "synthetic-free-top", "colleague.txt")
	w.assertClean(w.Local)

	t.Chdir(agent)
	w.resolveRestack(agent)
	if !w.contains(agent, "main", "synthetic-busy") || !w.contains(agent, "synthetic-busy", "synthetic-busy-top") {
		t.Error("the finished restack left its stack behind main")
	}
	if got := w.forkPoint("synthetic-busy"); got != w.tip(agent, "main") {
		t.Errorf("synthetic-busy is recorded as forking at %s, want main's tip", got)
	}
	if got := w.forkPoint("synthetic-busy-top"); got != w.tip(agent, "synthetic-busy") {
		t.Errorf("synthetic-busy-top is recorded as forking at %s, want synthetic-busy's tip", got)
	}
	w.assertHas(agent, "synthetic-busy", "busy.txt")
	w.assertHas(agent, "synthetic-busy-top", "shared.txt")
	w.assertHas(agent, "synthetic-busy-top", "colleague.txt")
	w.assertHas(agent, "synthetic-free-top", "free-top.txt")
	if got := w.git(agent, "rev-list", "--count", "main..synthetic-busy-top"); got != "2" {
		t.Errorf("the finished stack carries %s commits of its own, want its 2", got)
	}
	if structure := w.readStructure(); structure["synthetic-busy"] != "main" || structure["synthetic-busy-top"] != "synthetic-busy" || structure["synthetic-free"] != "main" || structure["synthetic-free-top"] != "synthetic-free" {
		t.Errorf("the recorded structure changed: %v", structure)
	}
	w.assertClean(agent)
	w.assertClean(w.Local)
	// Nothing is left unfinished: a command that waits for every restack runs.
	t.Chdir(w.Local)
	mustRun(t, "untrack", "--branch", "synthetic-free-top", "--scope", "subtree", "--apply")
}

func TestJourneyPullBesideARestackStoppedElsewhereThatIsThenAborted(t *testing.T) {
	w, agent, found := stoppedElsewhere(t)
	// Where the two forked before the restack: synthetic-busy from the trunk
	// both stacks began on, and its top from where it was found.
	began := w.git(w.Local, "merge-base", found["synthetic-busy"], w.tip(w.Local, "synthetic-free"))
	forked := map[string]string{"synthetic-busy": began, "synthetic-busy-top": found["synthetic-busy"]}

	mustRun(t, "pull", "--apply")
	pulled := w.tip(w.Local, "synthetic-free-top")
	w.assertClean(w.Local)

	t.Chdir(agent)
	mustRun(t, "restack", "--abort")
	for branch, tip := range found {
		if got := w.tip(agent, branch); got != tip {
			t.Errorf("%s is at %s after the abort, want %s where the restack found it", branch, got, tip)
		}
		if got := w.forkPoint(branch); got != forked[branch] {
			t.Errorf("%s is recorded as forking at %s after the abort, want %s", branch, got, forked[branch])
		}
	}
	if w.tip(agent, "main") != w.tip(w.Remote, "main") || w.tip(agent, "synthetic-free-top") != pulled {
		t.Error("aborting the agent's restack undid the pull beside it")
	}
	if got := w.forkPoint("synthetic-free"); got != w.tip(agent, "main") {
		t.Errorf("synthetic-free is recorded as forking at %s, want main's tip", got)
	}
	w.assertClean(agent)
	w.assertClean(w.Local)

	// The stack the abort put back is one main has moved past, as any stack
	// is after a pull beside it, and it restacks from there as any does.
	if _, _, err := run(t, "restack", "--apply"); err == nil {
		t.Fatal("the restack met no conflict the second time")
	}
	w.resolveRestack(agent)
	if !w.contains(agent, "main", "synthetic-busy-top") {
		t.Error("the stack could not be restacked onto main after the abort")
	}
	w.assertClean(agent)
}

// A pull that moves or reads what the stopped restack is moving still waits,
// as one in the restack's own worktree does, and moving the trunk alone does
// not.
func TestJourneyPullOverARestackStoppedElsewhereStillWaits(t *testing.T) {
	w, agent, _ := stoppedElsewhere(t)
	trunk := w.tip(w.Local, "main")

	refusedBy(t, "which holds synthetic-busy", "pull", "--branch", "synthetic-busy", "--apply")
	refusedBy(t, "which holds synthetic-busy", "pull", "--scope", "trunk", "--apply")
	if w.tip(w.Local, "main") != trunk {
		t.Error("a refused pull moved the trunk")
	}
	t.Chdir(agent)
	refusedBy(t, "already in progress", "pull", "--branch", "synthetic-free-top", "--apply")
	if w.tip(w.Local, "main") != trunk {
		t.Error("a pull refused in the restack's own worktree moved the trunk")
	}

	t.Chdir(w.Local)
	mustRun(t, "pull", "--trunk-only", "--apply")
	if w.tip(w.Local, "main") != w.tip(w.Remote, "main") {
		t.Error("pull --trunk-only did not bring main up to date")
	}
	w.assertClean(w.Local)
	t.Chdir(agent)
	mustRun(t, "restack", "--abort")
	w.assertClean(agent)
}

// prune forgets what landed in the stack it was asked about, and judges only
// that stack; one the stopped restack is moving cannot be judged until it is
// finished.
func TestJourneyPruneBesideARestackStoppedElsewhere(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-busy", "shared.txt")
	mustRun(t, "track", "--parent", "main", "--apply")
	w.branchOff("main", "synthetic-done", "done.txt")
	mustRun(t, "track", "--parent", "main", "--apply")
	w.commit(w.Local, "main", "shared.txt", "the trunk's own version")
	w.git(w.Local, "cherry-pick", "synthetic-done")

	agent := filepath.Join(filepath.Dir(w.Local), "agent")
	w.git(w.Local, "worktree", "add", "-q", agent, "synthetic-busy")
	t.Chdir(agent)
	if stdout, stderr, err := run(t, "restack", "--apply"); err == nil {
		t.Fatalf("the agent's restack did not stop on its conflict:\n%s%s", stdout, stderr)
	}
	refusedBy(t, "already in progress", "prune", "--branch", "synthetic-done", "--apply")

	t.Chdir(w.Local)
	refusedBy(t, "which holds synthetic-busy", "prune", "--branch", "synthetic-busy", "--apply")
	mustRun(t, "prune", "--branch", "synthetic-done", "--apply")
	if _, tracked := w.readStructure()["synthetic-done"]; tracked {
		t.Error("the landed branch is still recorded")
	}
	w.assertClean(w.Local)

	t.Chdir(agent)
	mustRun(t, "restack", "--abort")
	structure := w.readStructure()
	if _, tracked := structure["synthetic-done"]; tracked || structure["synthetic-busy"] != "main" {
		t.Errorf("aborting the agent's restack changed what was recorded beside it: %v", structure)
	}
	w.assertClean(agent)
}

// Adopting a stack records edges for its own branches. One built on a branch
// the stopped restack is moving would be recorded against where that branch
// no longer is.
func TestJourneyAdoptBesideARestackStoppedElsewhere(t *testing.T) {
	w, _, _ := stoppedElsewhere(t)
	w.git(w.Local, "switch", "-qc", "synthetic-over-busy", "synthetic-busy")
	w.commit(w.Local, "synthetic-over-busy", "over-busy.txt", "over-busy")
	refusedBy(t, "which holds synthetic-busy", "adopt", "--apply")
	if parent, tracked := w.readStructure()["synthetic-over-busy"]; tracked {
		t.Errorf("the refused adoption recorded synthetic-over-busy under %q", parent)
	}

	w.git(w.Local, "switch", "-q", "synthetic-free-top")
	mustRun(t, "pull", "--apply")
	w.git(w.Local, "switch", "-qc", "synthetic-over-free")
	w.commit(w.Local, "synthetic-over-free", "over-free.txt", "over-free")
	mustRun(t, "adopt", "--apply")
	if got := w.readStructure()["synthetic-over-free"]; got != "synthetic-free-top" {
		t.Errorf("synthetic-over-free is recorded under %q, want synthetic-free-top", got)
	}
	w.assertClean(w.Local)
}

// A command that cannot name what it would touch before it runs still waits
// for every restack, and says that is why.
func TestJourneyLandStillWaitsForARestackStoppedElsewhereAndSaysWhy(t *testing.T) {
	stoppedElsewhere(t)
	refusedBy(t, "cannot name the branches it would touch", "land", "--apply")
	refusedBy(t, "unfinished in checkout", "land", "--apply")
}

// Stepping along a stack only switches this checkout, so it waits for this
// worktree's restack and nobody else's.
func TestJourneyNavigationBesideARestackStoppedElsewhere(t *testing.T) {
	w, _, _ := stoppedElsewhere(t)
	mustRun(t, "down")
	if got := w.git(w.Local, "branch", "--show-current"); got != "synthetic-free" {
		t.Errorf("down left the checkout on %q, want synthetic-free", got)
	}
	w.assertClean(w.Local)
}

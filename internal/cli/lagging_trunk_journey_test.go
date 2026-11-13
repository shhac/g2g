package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// laggingTrunk is a trunk checked out with changes in the main checkout, a
// fetched remote ahead of it, and work in a second worktree: a branch started
// on the remote's trunk with create --at, a branch stacked on that, and a
// sibling recorded on the local trunk before it last moved. Nothing can bring
// the local trunk up to date, because its worktree is not free.
type laggingTrunk struct {
	*world
	fresh                          string
	pinned, above, older, upstream string
}

func newLaggingTrunk(t *testing.T) laggingTrunk {
	t.Helper()
	w := newWorld(t)
	w.commit(w.Other, "main", "upstream-1.txt", "upstream")
	w.commit(w.Other, "main", "upstream-2.txt", "upstream")
	w.git(w.Other, "push", "-q", "origin", "main")
	w.branchOff("main", "synthetic-older", "older.txt")
	mustRun(t, "track", "--parent", "main", "--apply")
	w.git(w.Local, "switch", "-q", "main")
	w.git(w.Local, "fetch", "-q", "origin")
	w.git(w.Local, "merge", "-q", "--ff-only", "origin/main~1")
	if err := os.WriteFile(filepath.Join(w.Local, "in-progress.txt"), []byte("synthetic\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.git(w.Local, "add", "in-progress.txt")

	fresh := filepath.Join(filepath.Dir(w.Local), "synthetic-fresh")
	w.git(w.Local, "worktree", "add", "-q", "--detach", fresh, "origin/main")
	t.Chdir(fresh)
	mustRun(t, "create", "synthetic-pinned", "--parent", "main", "--at", "origin/main", "--apply")
	w.commit(fresh, "synthetic-pinned", "pinned.txt", "pinned")
	mustRun(t, "create", "synthetic-above", "--apply")
	w.commit(fresh, "synthetic-above", "above.txt", "above")
	return laggingTrunk{world: w, fresh: fresh, pinned: "synthetic-pinned", above: "synthetic-above", older: "synthetic-older", upstream: w.tip(fresh, "origin/main")}
}

// assertOnUpstream checks a branch still carries the remote's trunk, which is
// what replaying it onto the lagging local copy would have taken out of it.
func (l laggingTrunk) assertOnUpstream(branch string) {
	l.t.Helper()
	if !l.contains(l.fresh, l.upstream, branch) {
		l.t.Errorf("%s no longer carries the remote's trunk", branch)
	}
}

// assertHeldTrunkUntouched checks the main checkout is exactly as it was left:
// main where it was and the staged change still the only one.
func (l laggingTrunk) assertHeldTrunkUntouched(main string) {
	l.t.Helper()
	if got := l.tip(l.Local, "main"); got != main {
		l.t.Errorf("held trunk moved from %s to %s", main, got)
	}
	if status := l.git(l.Local, "status", "--porcelain"); status != "A  in-progress.txt" {
		l.t.Errorf("main checkout changed:\n%s", status)
	}
}

func TestStatusReadsAStartOnTheRemoteTrunkAsALaggingTrunk(t *testing.T) {
	l := newLaggingTrunk(t)
	stdout := mustRun(t, "status", "--branch", l.pinned)
	if strings.Contains(stdout, "moved back") || strings.Contains(stdout, "--absorb") {
		t.Errorf("a lagging trunk read as moved back:\n%s", stdout)
	}
	for _, want := range []string{
		"main is 1 commit behind origin, where synthetic-pinned already starts",
		"g2g restack --branch synthetic-pinned --onto main",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("status does not say %q:\n%s", want, stdout)
		}
	}
	for _, line := range strings.Split(stdout, "\n") {
		if strings.Contains(line, l.pinned) && strings.Contains(line, "needs restack") {
			t.Errorf("a branch on the remote's trunk read as stale: %s", line)
		}
	}
}

// The branch on the remote's trunk is left alone; everything else in the
// stack that is genuinely stale still settles in the same restack.
func TestRestackSettlesTheRestOfAStackAroundALaggingTrunk(t *testing.T) {
	l := newLaggingTrunk(t)
	main := l.tip(l.Local, "main")
	l.commit(l.fresh, l.pinned, "pinned-more.txt", "pinned")
	pinned := l.tip(l.fresh, l.pinned)

	mustRun(t, "restack", "--branch", "main", "--scope", "stack", "--apply")

	if got := l.tip(l.fresh, l.pinned); got != pinned {
		t.Errorf("%s was replayed onto the lagging trunk", l.pinned)
	}
	l.assertOnUpstream(l.pinned)
	l.assertOnUpstream(l.above)
	if !l.contains(l.fresh, pinned, l.above) {
		t.Errorf("%s was not replayed onto %s", l.above, l.pinned)
	}
	l.assertHas(l.fresh, l.above, "pinned-more.txt")
	if !l.contains(l.fresh, main, l.older) {
		t.Errorf("%s was not replayed onto the local trunk", l.older)
	}
	l.assertClean(l.fresh)
	l.assertHeldTrunkUntouched(main)
}

func TestPullLeavingAHeldTrunkKeepsABranchOnTheRemoteTrunk(t *testing.T) {
	l := newLaggingTrunk(t)
	main, pinned := l.tip(l.Local, "main"), l.tip(l.fresh, l.pinned)

	mustRun(t, "pull", "--branch", l.pinned, "--apply")

	if got := l.tip(l.fresh, l.pinned); got != pinned {
		t.Errorf("%s was replayed onto the lagging trunk", l.pinned)
	}
	l.assertOnUpstream(l.above)
	l.assertClean(l.fresh)
	l.assertHeldTrunkUntouched(main)
	if _, err := os.Stat(filepath.Join(l.Local, ".git", "g2g", "restack.json")); err == nil {
		t.Error("pull left a restack journal")
	}
}

// A trunk rewound on purpose is asked for by name, and that still drops the
// commits the trunk no longer has, as it always has.
func TestRestackOntoTheTrunkByNameStillDropsWhatItLacks(t *testing.T) {
	l := newLaggingTrunk(t)
	stdout := mustRun(t, "restack", "--branch", l.pinned, "--onto", "main")
	if !strings.Contains(stdout, "Replays synthetic-pinned and synthetic-above onto main") || !strings.Contains(stdout, "will be dropped here too") {
		t.Errorf("--onto the lagging trunk did not offer the rewind:\n%s", stdout)
	}
}

// A fork past the trunk's tip that its remote never had is a trunk moved back
// by hand, not one that lags: the branch is stale, and restack replays it and
// drops the commit the trunk no longer has, as it always has.
func TestATrunkRewoundByHandIsNotReadAsLagging(t *testing.T) {
	w := newWorld(t)
	w.commit(w.Local, "main", "unpushed.txt", "local only")
	unpushed := w.tip(w.Local, "main")
	mustRun(t, "create", "synthetic-rewound", "--apply")
	w.commit(w.Local, "synthetic-rewound", "own.txt", "own")
	w.git(w.Local, "switch", "-q", "main")
	w.git(w.Local, "reset", "-q", "--hard", "origin/main")

	stdout := mustRun(t, "status", "--branch", "synthetic-rewound")
	if !strings.Contains(stdout, "needs restack") {
		t.Errorf("a branch on a commit the trunk dropped read as current:\n%s", stdout)
	}
	mustRun(t, "restack", "--branch", "synthetic-rewound", "--apply")
	if !w.contains(w.Local, "main", "synthetic-rewound") || w.contains(w.Local, unpushed, "synthetic-rewound") {
		t.Error("synthetic-rewound was not replayed onto main without the dropped commit")
	}
	w.assertHas(w.Local, "synthetic-rewound", "own.txt")
	w.assertClean(w.Local)
}

// A recorded branch deleted with plain Git, on a trunk that lags its remote,
// is doctor's to report. A status of another stack on that trunk never shows
// it, and failed outright asking Git about it.
func TestStatusOfAStackBesideADeletedRecordedBranch(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-gone", "gone.txt")
	mustRun(t, "track", "--parent", "main", "--apply")
	w.branchOff("main", "synthetic-kept", "kept.txt")
	mustRun(t, "track", "--parent", "main", "--apply")
	w.git(w.Local, "branch", "-q", "-D", "synthetic-gone")
	w.commit(w.Other, "main", "upstream.txt", "upstream")
	w.git(w.Other, "push", "-q", "origin", "main")
	w.git(w.Local, "fetch", "-q", "origin")

	for _, args := range [][]string{{"status"}, {"status", "--scope", "path"}} {
		stdout := mustRun(t, args...)
		if !strings.Contains(stdout, "synthetic-kept") {
			t.Errorf("g2g %s does not show the selected stack:\n%s", strings.Join(args, " "), stdout)
		}
	}
	doctor, _, _ := run(t, "doctor")
	if !strings.Contains(doctor, "synthetic-gone") {
		t.Errorf("doctor no longer reports the deleted branch:\n%s", doctor)
	}
}

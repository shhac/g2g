package cli_test

import (
	"strings"
	"testing"
)

// statusLine is the one line status draws for a branch.
func statusLine(t *testing.T, out, branch string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if fields := strings.Fields(line); len(fields) > 1 && fields[1] == branch && fields[0] != "Target" {
			return line
		}
	}
	t.Fatalf("status draws no line for %s:\n%s", branch, out)
	return ""
}

// status reads the remote from local refs alone, so it says what a fetch or a
// push last saw — through a branch's whole life, and across a pull, which
// fetches into g2g's own refs and leaves origin/main where it was. Read from
// origin/main alone, the trunk would claim to be ahead by everything pulled.
func TestJourneyStatusSaysWhereEachBranchStandsAgainstTheRemote(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")

	out := mustRun(t, "status", "--no-links")
	if line := statusLine(t, out, "synthetic-a"); !strings.Contains(line, "not on origin") {
		t.Errorf("an unpushed branch reads %q", line)
	}
	if line := statusLine(t, out, "main"); !strings.Contains(line, "origin✓") {
		t.Errorf("the trunk reads %q, want it level with origin", line)
	}
	if !strings.Contains(out, "g2g push") {
		t.Errorf("status does not say how to publish:\n%s", out)
	}

	mustRun(t, "push", "--apply")
	if line := statusLine(t, mustRun(t, "status"), "synthetic-a"); !strings.Contains(line, "origin✓") {
		t.Errorf("a pushed branch reads %q", line)
	}

	w.commit(w.Local, "synthetic-a", "more.txt", "more")
	if line := statusLine(t, mustRun(t, "status"), "synthetic-a"); !strings.Contains(line, "1 ahead") {
		t.Errorf("a branch with one unpushed commit reads %q", line)
	}
	mustRun(t, "push", "--apply")

	w.commit(w.Other, "main", "trunk.txt", "trunk")
	w.git(w.Other, "push", "-q", "origin", "main")
	mustRun(t, "pull", "--apply")

	out = mustRun(t, "status")
	if line := statusLine(t, out, "main"); !strings.Contains(line, "origin✓") {
		t.Errorf("after a pull the trunk reads %q, want it level with what was pulled", line)
	}
	if line := statusLine(t, out, "synthetic-a"); !strings.Contains(line, "replayed since pushed") {
		t.Errorf("a replayed branch reads %q", line)
	}
	if !strings.Contains(out, "nothing was asked of the network") {
		t.Errorf("status does not say how fresh its answer is:\n%s", out)
	}
	w.assertClean(w.Local)
}

// A remote named on purpose that does not exist is a mistake; the default one
// missing is a repository nothing has been published from.
func TestJourneyStatusRefusesARemoteNamedOnPurposeOnly(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")

	if _, _, err := run(t, "status", "--remote", "synthetic-nowhere"); err == nil {
		t.Error("status --remote synthetic-nowhere error = nil")
	}
	w.git(w.Local, "remote", "remove", "origin")
	out := mustRun(t, "status")
	if strings.Contains(out, "origin") {
		t.Errorf("a repository with no remote was compared with one:\n%s", out)
	}
}

// A colleague's commit on the branch, once fetched, is work the branch does not
// have; one of ours on top of that makes it a divergence, which is the one
// remote finding doctor reports, since it is what a push would refuse.
func TestJourneyStatusSaysBehindThenDiverged(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "push", "--apply")

	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "switch", "-q", "-c", "synthetic-a", "origin/synthetic-a")
	w.commit(w.Other, "synthetic-a", "review.txt", "review")
	w.git(w.Other, "push", "-q", "origin", "synthetic-a")
	w.git(w.Local, "fetch", "-q", "origin")

	out := mustRun(t, "status")
	if line := statusLine(t, out, "synthetic-a"); !strings.Contains(line, "origin✗ 1 behind") {
		t.Errorf("a branch behind its remote reads %q", line)
	}
	if !strings.Contains(out, "g2g pull") {
		t.Errorf("status does not say how to catch up:\n%s", out)
	}

	w.commit(w.Local, "synthetic-a", "mine.txt", "mine")
	if line := statusLine(t, mustRun(t, "status"), "synthetic-a"); !strings.Contains(line, "diverged · 1 here, 1 there") {
		t.Errorf("a diverged branch reads %q", line)
	}
	out, _, err := run(t, "doctor", "--no-links")
	if err == nil || !strings.Contains(out, "synthetic-a: diverged from origin · 1 here, 1 there · run g2g pull --branch synthetic-a.") {
		t.Errorf("doctor on a divergence: %v\n%s", err, out)
	}
	w.assertClean(w.Local)
}

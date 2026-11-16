package cli_test

import (
	"strings"
	"testing"
)

func TestJourneySubmitRefusesStaleLocalPreservedBase(t *testing.T) {
	w := newWorld(t)
	w.git(w.Local, "branch", "-m", "main", "synthetic-trunk")
	root := w.tip(w.Local, "synthetic-trunk")
	w.branchOff("synthetic-trunk", "synthetic-head", "head.txt")
	retained := w.tip(w.Local, "synthetic-head")
	w.commit(w.Local, "synthetic-head", "dropped.txt", "synthetic dropped")
	mustRun(t, "track", "--parent", "synthetic-trunk", "--apply")
	mustRun(t, "push", "--apply")
	before := w.tip(w.Remote, "synthetic-head")
	w.git(w.Local, "push", "origin", retained+":refs/heads/synthetic-base")
	w.git(w.Local, "branch", "synthetic-base", root)
	landingGitHubPulls(t, w.Remote, [2]string{"synthetic-head", "synthetic-base"})
	// Drop the last published commit deliberately. Push permits this known drop;
	// submit must still catch the irreversible merge into the unchanged PR base.
	w.git(w.Local, "switch", "synthetic-trunk")
	w.git(w.Local, "branch", "-f", "synthetic-head", retained)
	w.git(w.Local, "switch", "synthetic-head")
	if w.contains(w.Local, "synthetic-head", "synthetic-base") || !w.contains(w.Remote, retained, "synthetic-base") {
		t.Fatal("base knowledge is not stale")
	}
	args := []string{"submit", "--branch", "synthetic-head", "--no-template", "--no-comment"}
	preview := mustRun(t, args...)
	if !strings.Contains(preview, "preserved base synthetic-base would contain") {
		t.Fatalf("unsafe remote ancestry was allowed:\n%s", preview)
	}
	if out, stderr, err := run(t, append(args, "--apply")...); err == nil {
		t.Fatalf("unsafe publication applied:\n%s%s", out, stderr)
	}
	if w.tip(w.Remote, "synthetic-head") != before {
		t.Error("refusal published the head")
	}
	w.assertClean(w.Local)
}

func TestJourneySubmitReorderedBottomLeavesRemoteBaseUnchanged(t *testing.T) {
	w := newWorld(t)
	w.git(w.Local, "branch", "-m", "main", "synthetic-trunk")
	w.branchOff("synthetic-trunk", "synthetic-r", "r.txt")
	w.branchOff("synthetic-r", "synthetic-s", "s.txt")
	w.branchOff("synthetic-s", "synthetic-n", "n.txt")
	for _, edge := range [][2]string{{"synthetic-r", "synthetic-trunk"}, {"synthetic-s", "synthetic-r"}, {"synthetic-n", "synthetic-s"}} {
		mustRun(t, "track", "--branch", edge[0], "--parent", edge[1], "--apply")
	}
	mustRun(t, "push", "--apply")
	landingGitHubPulls(t, w.Remote, [2]string{"synthetic-r", "synthetic-trunk"}, [2]string{"synthetic-s", "synthetic-r"}, [2]string{"synthetic-n", "synthetic-s"})
	before := w.tip(w.Remote, "synthetic-s")
	mustRun(t, "restack", "--branch", "synthetic-n", "--scope", "branch", "--onto", "synthetic-trunk", "--apply")
	mustRun(t, "restack", "--branch", "synthetic-r", "--onto", "synthetic-n", "--apply")
	w.assertClean(w.Local)
	if !w.contains(w.Local, "synthetic-n", "synthetic-s") || w.contains(w.Local, "synthetic-n", before) {
		t.Fatal("reorder did not separate local and published base ancestry")
	}
	// The bottom's path selects only it. Submit offers stack/path, so this
	// exercises branch-only publication through the existing CLI scopes.
	args := []string{"submit", "--branch", "synthetic-n", "--scope", "path", "--no-template", "--no-comment"}
	preview := mustRun(t, args...)
	if strings.Contains(preview, "would contain") {
		t.Fatalf("safe partial publication refused:\n%s", preview)
	}
	mustRun(t, append(args, "--apply")...)
	if w.tip(w.Remote, "synthetic-s") != before || w.tip(w.Remote, "synthetic-n") != w.tip(w.Local, "synthetic-n") {
		t.Error("partial publication moved the wrong refs")
	}
	w.assertClean(w.Local)
}

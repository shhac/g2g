package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/cli"
)

func TestJourneySubmitAfterThreeRestacksAndAConflictResolution(t *testing.T) {
	w := newWorld(t)
	for _, dir := range []string{w.Local, w.Other} {
		w.git(dir, "branch", "-m", "main", "synthetic-main")
	}
	w.git(w.Local, "push", "-qu", "origin", "synthetic-main")
	w.branchOff("synthetic-main", "synthetic-bottom", "bottom.txt")
	w.branchOff("synthetic-bottom", "synthetic-middle", "middle.txt")
	w.branchOff("synthetic-middle", "synthetic-top", "shared.txt")
	for _, edge := range [][2]string{{"synthetic-bottom", "synthetic-main"}, {"synthetic-middle", "synthetic-bottom"}, {"synthetic-top", "synthetic-middle"}} {
		mustRun(t, "track", "--branch", edge[0], "--parent", edge[1], "--apply")
	}
	mustRun(t, "push", "--scope", "stack", "--apply")
	landingGitHubPulls(t, w.Remote, [2]string{"synthetic-bottom", "synthetic-main"}, [2]string{"synthetic-middle", "synthetic-bottom"}, [2]string{"synthetic-top", "synthetic-middle"})
	agreement := w.tip(w.Local, "refs/g2g/synced/origin/synthetic-top")
	w.commit(w.Other, "synthetic-main", "shared.txt", "synthetic upstream side")
	w.git(w.Other, "push", "-q", "origin", "synthetic-main")
	w.git(w.Local, "fetch", "-q", "origin")

	out, _, err := run(t, "restack", "--branch", "synthetic-bottom", "--scope", "stack", "--onto", "origin/synthetic-main", "--apply")
	if !cli.StoppedPartWayForTest(err) {
		t.Fatalf("first restack did not stop on the top conflict: %v\n%s", err, out)
	}
	if got := w.git(w.Local, "diff", "--name-only", "--diff-filter=U"); got != "shared.txt" {
		t.Fatalf("conflicts = %q, want shared.txt", got)
	}
	if err := os.WriteFile(filepath.Join(w.Local, "shared.txt"), []byte("synthetic upstream side\nshared.txt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.git(w.Local, "add", "shared.txt")
	mustRun(t, "restack", "--continue")
	w.assertClean(w.Local)
	if got := w.tip(w.Local, "refs/g2g/synced/origin/synthetic-top"); got != agreement {
		t.Fatal("restack changed the last agreement")
	}

	w.commit(w.Local, "synthetic-bottom", "fix.txt", "synthetic lower fix")
	mustRun(t, "restack", "--branch", "synthetic-bottom", "--scope", "stack", "--apply")
	w.assertClean(w.Local)
	w.commit(w.Other, "synthetic-main", "later.txt", "synthetic later trunk work")
	w.git(w.Other, "push", "-q", "origin", "synthetic-main")
	w.git(w.Local, "fetch", "-q", "origin")
	mustRun(t, "restack", "--branch", "synthetic-bottom", "--scope", "stack", "--onto", "origin/synthetic-main", "--apply")
	w.assertClean(w.Local)

	args := []string{"submit", "--branch", "synthetic-top", "--scope", "stack", "--no-template", "--no-comment"}
	preview := mustRun(t, args...)
	if strings.Contains(preview, "Apply blocked") {
		t.Fatalf("g2g refuses its own repeated replay:\n%s\noriginal: %s\nreplayed: %s", preview,
			w.git(w.Local, "show", "-s", "--format=%H %an %ae %at %ct %s", agreement),
			w.git(w.Local, "show", "-s", "--format=%H %an %ae %at %ct %s", "synthetic-top"))
	}
	mustRun(t, append(args, "--apply")...)
	for _, branch := range []string{"synthetic-bottom", "synthetic-middle", "synthetic-top"} {
		if w.tip(w.Remote, branch) != w.tip(w.Local, branch) {
			t.Errorf("%s was not published", branch)
		}
	}
	if got := w.git(w.Remote, "show", "synthetic-top:shared.txt"); got != "synthetic upstream side\nshared.txt" {
		t.Errorf("resolution did not preserve both sides: %q", got)
	}
	w.assertClean(w.Local)
}

func TestJourneySubmitAfterPlainPushAndRepeatedReplay(t *testing.T) {
	plainPushReplay(t, false, false, false)
}

func TestJourneyPushAfterPlainPushReplayProtectsUnseenReviewer(t *testing.T) {
	plainPushReplay(t, false, true, false)
}

func TestJourneyPushAfterPlainPushReplayExplainsMissingAgreement(t *testing.T) {
	plainPushReplay(t, true, false, false)
}

func TestJourneyPullAfterPlainPushPreservesAgreementThroughReplayConflict(t *testing.T) {
	plainPushReplay(t, false, false, true)
}

func plainPushReplay(t *testing.T, ahead, reviewer, pull bool) {
	t.Helper()
	w := newWorld(t)
	for _, dir := range []string{w.Local, w.Other} {
		w.git(dir, "branch", "-m", "main", "synthetic-main")
	}
	w.git(w.Local, "push", "-qu", "origin", "synthetic-main")
	w.branchOff("synthetic-main", "synthetic-bottom", "bottom.txt")
	w.branchOff("synthetic-bottom", "synthetic-middle", "middle.txt")
	w.branchOff("synthetic-middle", "synthetic-top", "shared.txt")
	for _, edge := range [][2]string{{"synthetic-bottom", "synthetic-main"}, {"synthetic-middle", "synthetic-bottom"}, {"synthetic-top", "synthetic-middle"}} {
		mustRun(t, "track", "--branch", edge[0], "--parent", edge[1], "--apply")
	}
	w.git(w.Local, "push", "-q", "origin", "synthetic-bottom", "synthetic-middle", "synthetic-top")
	w.git(w.Local, "fetch", "-q", "origin")
	landingGitHubPulls(t, w.Remote, [2]string{"synthetic-bottom", "synthetic-main"}, [2]string{"synthetic-middle", "synthetic-bottom"}, [2]string{"synthetic-top", "synthetic-middle"})
	agreement := w.tip(w.Local, "synthetic-top")
	if ahead {
		w.commit(w.Local, "synthetic-top", "local.txt", "synthetic local addition")
	}
	w.commit(w.Other, "synthetic-main", "shared.txt", "synthetic upstream side")
	w.git(w.Other, "push", "-q", "origin", "synthetic-main")
	w.git(w.Local, "fetch", "-q", "origin")

	var reviewerTip string
	if reviewer {
		w.git(w.Other, "fetch", "-q", "origin")
		w.git(w.Other, "switch", "-qc", "synthetic-top", "origin/synthetic-top")
		w.commit(w.Other, "synthetic-top", "reviewer.txt", "synthetic reviewer addition")
		reviewerTip = w.tip(w.Other, "synthetic-top")
		w.git(w.Other, "push", "-q", "origin", "synthetic-top")
	}
	command := []string{"restack", "--branch", "synthetic-bottom", "--scope", "stack", "--onto", "origin/synthetic-main", "--apply"}
	if pull {
		command = []string{"pull", "--branch", "synthetic-bottom", "--scope", "stack", "--apply"}
	}
	out, _, err := run(t, command...)
	if !cli.StoppedPartWayForTest(err) {
		t.Fatalf("first restack did not stop on the top conflict: %v\n%s", err, out)
	}
	point := w.git(w.Local, "for-each-ref", "--format=%(objectname)", "refs/g2g/synced/origin/synthetic-top")
	if (!ahead && point != agreement) || (ahead && point != "") {
		t.Fatalf("agreement before conflict recovery = %q; initial published tip = %s, ahead=%t", point, agreement, ahead)
	}
	if got := w.git(w.Local, "for-each-ref", "--format=%(refname)", "refs/g2g/synced/origin/synthetic-main"); got != "" {
		t.Fatalf("trunk received a sync point: %s", got)
	}
	if got := w.git(w.Local, "diff", "--name-only", "--diff-filter=U"); got != "shared.txt" {
		t.Fatalf("conflicts = %q, want shared.txt", got)
	}
	if err := os.WriteFile(filepath.Join(w.Local, "shared.txt"), []byte("synthetic upstream side\nshared.txt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.git(w.Local, "add", "shared.txt")
	mustRun(t, "restack", "--continue")
	w.assertClean(w.Local)

	w.commit(w.Local, "synthetic-bottom", "fix.txt", "synthetic lower fix")
	mustRun(t, "restack", "--branch", "synthetic-bottom", "--scope", "stack", "--apply")
	w.assertClean(w.Local)
	w.commit(w.Other, "synthetic-main", "later.txt", "synthetic later trunk work")
	w.git(w.Other, "push", "-q", "origin", "synthetic-main")
	w.git(w.Local, "fetch", "-q", "origin")
	mustRun(t, "restack", "--branch", "synthetic-bottom", "--scope", "stack", "--onto", "origin/synthetic-main", "--apply")
	w.assertClean(w.Local)

	if got := w.git(w.Local, "for-each-ref", "--format=%(objectname)", "refs/g2g/synced/origin/synthetic-top"); got != point {
		t.Fatalf("repeated replay changed agreement: %q -> %q", point, got)
	}
	args := []string{"submit", "--branch", "synthetic-top", "--scope", "stack", "--no-template", "--no-comment"}
	preview := mustRun(t, args...)
	if ahead || reviewer {
		if !strings.Contains(preview, "the published version differs on synthetic-top") {
			t.Fatalf("expected refusal: %s", preview)
		}
		if ahead && !strings.Contains(preview, "g2g has no record that this checkout and the remote agreed on synthetic-top") {
			t.Fatalf("refusal hides missing agreement: %s", preview)
		}
		if reviewer && (!strings.Contains(preview, reviewerTip[:12]) || !strings.Contains(preview, "synthetic reviewer.txt")) {
			t.Fatalf("refusal hides reviewer commit: %s", preview)
		}
		if _, _, err := run(t, "push", "--branch", "synthetic-top", "--scope", "stack", "--apply"); err == nil {
			t.Fatal("push replaced differing remote work")
		}
		if reviewer && w.tip(w.Remote, "synthetic-top") != reviewerTip {
			t.Fatal("push erased reviewer work")
		}
		w.assertClean(w.Local)
		return
	}
	if strings.Contains(preview, "Apply blocked") {
		t.Fatalf("g2g refuses its own repeated replay:\n%s\noriginal: %s\nreplayed: %s", preview,
			w.git(w.Local, "show", "-s", "--format=%H %an %ae %at %ct %s", agreement),
			w.git(w.Local, "show", "-s", "--format=%H %an %ae %at %ct %s", "synthetic-top"))
	}
	mustRun(t, append(args, "--apply")...)
	for _, branch := range []string{"synthetic-bottom", "synthetic-middle", "synthetic-top"} {
		if w.tip(w.Remote, branch) != w.tip(w.Local, branch) {
			t.Errorf("%s was not published", branch)
		}
	}
	if got := w.git(w.Remote, "show", "synthetic-top:shared.txt"); got != "synthetic upstream side\nshared.txt" {
		t.Errorf("resolution did not preserve both sides: %q", got)
	}
	w.assertClean(w.Local)
}

func TestJourneyPushWithoutAgreementExplainsUnfetchedPublishedVersion(t *testing.T) {
	w := newWorld(t)
	w.git(w.Local, "branch", "-m", "main", "synthetic-main")
	w.git(w.Local, "push", "-q", "origin", "synthetic-main")
	w.branchOff("synthetic-main", "synthetic-top", "top.txt")
	mustRun(t, "track", "--branch", "synthetic-top", "--parent", "synthetic-main", "--apply")
	w.git(w.Local, "push", "-q", "origin", "synthetic-top")
	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "switch", "-qc", "synthetic-top", "origin/synthetic-top")
	w.commit(w.Other, "synthetic-top", "reviewer.txt", "synthetic reviewer addition")
	w.git(w.Other, "push", "-q", "origin", "synthetic-top")
	published := w.tip(w.Remote, "synthetic-top")
	w.commit(w.Local, "synthetic-top", "local.txt", "synthetic local addition")
	preview := mustRun(t, "push", "--branch", "synthetic-top")
	if !strings.Contains(preview, "g2g has no record that this checkout and the remote agreed on synthetic-top") {
		t.Fatalf("unfetched version hides missing agreement: %s", preview)
	}
	if _, _, err := run(t, "push", "--branch", "synthetic-top", "--apply"); err == nil {
		t.Fatal("push overwrote unfetched reviewer work")
	}
	if got := w.tip(w.Remote, "synthetic-top"); got != published {
		t.Fatal("remote moved despite refusal")
	}
	w.assertClean(w.Local)
}

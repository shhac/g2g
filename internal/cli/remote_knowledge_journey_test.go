package cli_test

import (
	"strings"
	"testing"
)

func TestJourneyStatusAdmitsConflictingRemoteRecordsAfterFollowingARewrite(t *testing.T) {
	w := newWorld(t)
	oldTrunk := w.tip(w.Local, "main")
	w.commit(w.Local, "main", "trunk.txt", "synthetic trunk advance")
	w.git(w.Local, "push", "-q", "origin", "main")
	newTrunk := w.tip(w.Local, "main")
	w.branchOff("main", "synthetic-parent", "parent.txt")
	mustRun(t, "track", "--parent", "main", "--apply")
	w.branchOff("synthetic-parent", "synthetic-child", "child.txt")
	mustRun(t, "track", "--parent", "synthetic-parent", "--apply")
	mustRun(t, "push", "--apply")
	tracking := w.tip(w.Local, "refs/remotes/origin/synthetic-parent")

	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "switch", "-qc", "synthetic-parent", "origin/synthetic-parent")
	w.git(w.Other, "rebase", "--onto", oldTrunk, newTrunk, "synthetic-parent")
	w.commit(w.Other, "synthetic-parent", "review.txt", "synthetic review")
	w.git(w.Other, "push", "-q", "--force", "origin", "synthetic-parent")

	mustRun(t, "pull", "--above", "synthetic-parent")
	mustRun(t, "pull", "--above", "synthetic-parent", "--apply")
	if w.tip(w.Local, "synthetic-parent") != w.tip(w.Remote, "synthetic-parent") {
		t.Fatal("pull did not take the published parent")
	}
	out := mustRun(t, "status")
	if !strings.Contains(statusLine(t, out, "synthetic-parent"), "conflicting local records") || !strings.Contains(out, "git fetch origin") {
		t.Fatalf("status hid conflicting knowledge: %s", out)
	}
	if strings.Contains(out, "origin dropped commits synthetic-parent") {
		t.Fatalf("status advised dropping work using stale knowledge: %s", out)
	}
	if w.tip(w.Local, "refs/remotes/origin/synthetic-parent") != tracking {
		t.Fatal("pull or status moved the user's tracking ref")
	}
	w.git(w.Local, "fetch", "-q", "origin")
	out = mustRun(t, "status")
	if !strings.Contains(statusLine(t, out, "synthetic-parent"), "origin✓") || strings.Contains(out, "conflicting local records") {
		t.Fatalf("status did not recognize refreshed knowledge: %s", out)
	}
	w.assertClean(w.Local)
}

func TestJourneyDoctorReportsConflictingKnowledgeWithoutCallingTheBranchDiverged(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-parent", "parent.txt")
	mustRun(t, "track", "--parent", "main", "--apply")
	mustRun(t, "push", "--apply")
	old := w.tip(w.Local, "synthetic-parent")
	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "switch", "-qc", "synthetic-parent", "origin/synthetic-parent")
	w.git(w.Other, "commit", "-q", "--amend", "-m", "synthetic revised identity")
	w.git(w.Other, "push", "-q", "--force", "origin", "synthetic-parent")
	// Fetch through g2g's isolated namespace without editing tracking refs.
	w.git(w.Local, "fetch", "-q", "--refmap=", "--no-write-fetch-head", "origin", "+refs/heads/synthetic-parent:refs/g2g/remotes/origin/synthetic-parent")
	out, _, err := run(t, "doctor")
	if err == nil || !strings.Contains(out, "conflicting local remote records") || !strings.Contains(out, "git fetch origin") || strings.Contains(out, "diverged from origin") {
		t.Fatalf("doctor misdiagnosed uncertain knowledge: %v\n%s", err, out)
	}
	if w.tip(w.Local, "refs/remotes/origin/synthetic-parent") != old {
		t.Fatal("doctor moved a tracking ref")
	}
}

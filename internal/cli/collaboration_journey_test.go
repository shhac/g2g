package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateAtKnownUpstreamLeavesHeldTrunkUntouched(t *testing.T) {
	w := newWorld(t)
	before := w.tip(w.Local, "main")
	w.commit(w.Other, "main", "upstream.txt", "upstream")
	w.git(w.Other, "push", "-q", "origin", "main")
	w.git(w.Local, "fetch", "-q", "origin")
	start := w.tip(w.Local, "origin/main")
	fresh := filepath.Join(filepath.Dir(w.Local), "synthetic-fresh")
	w.git(w.Local, "worktree", "add", "-q", "--detach", fresh, "origin/main")
	t.Chdir(fresh)
	mustRun(t, "create", "synthetic-new", "--parent", "main", "--at", "origin/main", "--apply")
	if got := w.tip(fresh, "synthetic-new"); got != start {
		t.Fatalf("start = %s, want %s", got, start)
	}
	if got := w.tip(w.Local, "main"); got != before {
		t.Fatal("held trunk moved")
	}
	fork := w.git(fresh, "rev-parse", "refs/g2g/forkpoints/synthetic-new")
	if fork != start {
		t.Fatalf("fork = %s, want upstream %s", fork, start)
	}
	w.assertClean(w.Local)
	w.assertClean(fresh)
}

func TestCreateAtRefusesUnprovenInheritedWork(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-feature", "feature.txt")
	mustRun(t, "track", "--parent", "main", "--apply")
	w.branchOff("synthetic-feature", "synthetic-newer", "newer.txt")
	for _, parent := range []string{"main", "synthetic-feature"} {
		stdout, _, err := run(t, "create", "synthetic-refused", "--parent", parent, "--at", "synthetic-newer", "--apply")
		if err == nil {
			t.Fatalf("accepted unsupported inherited work under %s:\n%s", parent, stdout)
		}
	}
	if strings.Contains(w.git(w.Local, "branch", "--format=%(refname:short)"), "synthetic-refused") {
		t.Fatal("refused branch was created")
	}
}

func TestPullAbovePreservesPublishedParentAndOriginalSelection(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--parent", "main", "--apply")
	mustRun(t, "push", "--apply")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	mustRun(t, "track", "--parent", "synthetic-a", "--apply")
	w.branchOff("synthetic-b", "synthetic-c", "c.txt")
	mustRun(t, "track", "--parent", "synthetic-b", "--apply")
	w.branchOff("synthetic-a", "synthetic-uncle", "uncle.txt")
	mustRun(t, "track", "--parent", "synthetic-a", "--apply")
	w.git(w.Local, "switch", "-q", "synthetic-c")
	beforeMain, beforeUncle := w.tip(w.Local, "main"), w.tip(w.Local, "synthetic-uncle")
	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "switch", "-q", "-c", "synthetic-a", "origin/synthetic-a")
	w.commit(w.Other, "synthetic-a", "published.txt", "published")
	w.git(w.Other, "push", "-q", "origin", "synthetic-a")
	published := w.tip(w.Other, "synthetic-a")
	w.commit(w.Other, "main", "new-trunk.txt", "new trunk")
	w.git(w.Other, "push", "-q", "origin", "main")
	mustRun(t, "pull", "--above", "synthetic-a", "--apply")
	if w.tip(w.Local, "synthetic-a") != published {
		t.Fatal("published boundary was replayed")
	}
	if w.tip(w.Local, "main") != beforeMain || w.tip(w.Local, "synthetic-uncle") != beforeUncle {
		t.Fatal("an unselected ancestor or uncle moved")
	}
	for _, pair := range [][2]string{{"synthetic-a", "synthetic-b"}, {"synthetic-b", "synthetic-c"}} {
		if !w.contains(w.Local, pair[0], pair[1]) || w.git(w.Local, "rev-list", "--count", pair[0]+".."+pair[1]) != "1" {
			t.Fatalf("incorrect own range %v", pair)
		}
	}
	if w.syncedTip(w.Local, "synthetic-a") != published {
		t.Fatal("boundary agreement not recorded")
	}
	w.assertClean(w.Local)
}

func TestPullAboveRefusesDivergenceUnlessExplicitlyTaken(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--parent", "main", "--apply")
	mustRun(t, "push", "--apply")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	mustRun(t, "track", "--parent", "synthetic-a", "--apply")
	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "switch", "-q", "-c", "synthetic-a", "origin/synthetic-a")
	w.commit(w.Other, "synthetic-a", "theirs.txt", "theirs")
	w.git(w.Other, "push", "-q", "origin", "synthetic-a")
	published := w.tip(w.Other, "synthetic-a")
	w.commit(w.Local, "synthetic-a", "mine.txt", "mine")
	w.git(w.Local, "switch", "-q", "synthetic-b")
	stdout, _, err := run(t, "pull", "--above", "synthetic-a", "--apply")
	if err == nil || !strings.Contains(stdout, "--above synthetic-a") {
		t.Fatalf("missing scoped divergence repair: %v\n%s", err, stdout)
	}
	mustRun(t, "pull", "--above", "synthetic-a", "--take", "published", "--through", "synthetic-a", "--apply")
	if w.tip(w.Local, "synthetic-a") != published {
		t.Fatal("taken parent differs from published tip")
	}
	if !w.contains(w.Local, "synthetic-a", "synthetic-b") {
		t.Fatal("child did not follow")
	}
	w.assertClean(w.Local)
}

func TestPullAboveLeavesDirtyBoundaryWithoutRecordingAgreement(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--parent", "main", "--apply")
	mustRun(t, "push", "--apply")
	before := w.tip(w.Local, "synthetic-a")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	mustRun(t, "track", "--parent", "synthetic-a", "--apply")
	held := filepath.Join(filepath.Dir(w.Local), "synthetic-held")
	w.git(w.Local, "worktree", "add", "-q", held, "synthetic-a")
	if err := os.WriteFile(filepath.Join(held, "dirty.txt"), []byte("wip"), 0600); err != nil {
		t.Fatal(err)
	}
	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "switch", "-q", "-c", "synthetic-a", "origin/synthetic-a")
	w.commit(w.Other, "synthetic-a", "theirs.txt", "theirs")
	w.git(w.Other, "push", "-q", "origin", "synthetic-a")
	stdout := mustRun(t, "pull", "--above", "synthetic-a", "--apply")
	if strings.Contains(stdout, "Follows synthetic-a as published") || !strings.Contains(stdout, "current local tip") || strings.Contains(stdout, "pull --trunk-only") {
		t.Fatalf("inaccurate left-boundary advice:\n%s", stdout)
	}
	if w.tip(w.Local, "synthetic-a") != before || w.syncedTip(w.Local, "synthetic-a") != before {
		t.Fatal("left boundary was moved or falsely agreed")
	}
}

func TestPullEmptiedBranchExplainsPinnedReplacement(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		t.Run(map[bool]string{false: "emptied", true: "recreated"}[replacement], func(t *testing.T) {
			w := newWorld(t)
			w.branchOff("main", "synthetic-a", "a.txt")
			mustRun(t, "track", "--parent", "main", "--apply")
			mustRun(t, "push", "--apply")
			published := w.tip(w.Local, "synthetic-a")
			w.git(w.Local, "reset", "--hard", "main")
			if replacement {
				w.commit(w.Local, "synthetic-a", "replacement.txt", "unrelated new series")
			}
			stdout, _, err := run(t, "pull", "--apply")
			if err == nil || !strings.Contains(stdout, "g2g push will refuse") || !strings.Contains(stdout, "--force-with-lease=refs/heads/synthetic-a:"+published) {
				t.Fatalf("inaccurate emptied-branch advice: %v\n%s", err, stdout)
			}
			if strings.Contains(stdout, "for g2g push to publish") {
				t.Fatal("pull still promises automatic publication")
			}

		})
	}
}

func TestPullAboveConflictRecoveryKeepsBoundaryDurable(t *testing.T) {
	for _, verb := range []string{"continue", "skip", "abort"} {
		t.Run(verb, func(t *testing.T) {
			w := newWorld(t)
			w.branchOff("main", "synthetic-a", "shared.txt")
			mustRun(t, "track", "--parent", "main", "--apply")
			mustRun(t, "push", "--apply")
			w.git(w.Local, "switch", "-q", "-c", "synthetic-b", "synthetic-a")
			w.commit(w.Local, "synthetic-b", "shared.txt", "child")
			mustRun(t, "track", "--parent", "synthetic-a", "--apply")
			w.branchOff("synthetic-b", "synthetic-c", "c.txt")
			mustRun(t, "track", "--parent", "synthetic-b", "--apply")
			oldMain, oldB, oldC := w.tip(w.Local, "main"), w.tip(w.Local, "synthetic-b"), w.tip(w.Local, "synthetic-c")
			w.git(w.Other, "fetch", "-q", "origin")
			w.git(w.Other, "switch", "-q", "-c", "synthetic-a", "origin/synthetic-a")
			w.commit(w.Other, "synthetic-a", "shared.txt", "publisher")
			w.git(w.Other, "push", "-q", "origin", "synthetic-a")
			published := w.tip(w.Other, "synthetic-a")
			// If recovery forgets the boundary, main's advanced tip tempts it to
			// replay the publisher's branch while resuming the child's conflict.
			w.commit(w.Local, "main", "trunk.txt", "trunk")
			localMain := w.tip(w.Local, "main")
			w.git(w.Local, "switch", "-q", "synthetic-c")
			stdout, _, err := run(t, "pull", "--above", "synthetic-a", "--apply")
			if err == nil {
				t.Fatalf("expected child conflict:\n%s", stdout)
			}
			record, err := os.ReadFile(filepath.Join(w.Local, ".git", "g2g", "restack.json"))
			if err != nil || !strings.Contains(string(record), `"above": "synthetic-a"`) || !strings.Contains(string(record), `"schemaVersion": 2`) {
				t.Fatalf("durable boundary missing: %s %v", record, err)
			}
			if verb == "continue" {
				if err := os.WriteFile(filepath.Join(w.Local, "shared.txt"), []byte("resolved\n"), 0600); err != nil {
					t.Fatal(err)
				}
				w.git(w.Local, "add", "shared.txt")
			}
			mustRun(t, "restack", "--"+verb)
			if w.tip(w.Local, "synthetic-a") != published || w.tip(w.Local, "main") != localMain {
				t.Fatal("recovery moved protected boundary or trunk")
			}
			if oldMain == localMain {
				t.Fatal("test did not advance trunk")
			}
			if verb == "abort" {
				if w.tip(w.Local, "synthetic-b") != oldB || w.tip(w.Local, "synthetic-c") != oldC {
					t.Fatal("abort did not restore child tips")
				}
			} else if !w.contains(w.Local, "synthetic-a", "synthetic-c") {
				t.Fatal("resumed descendants did not follow boundary")
			}
			if _, err := os.Stat(filepath.Join(w.Local, ".git", "g2g", "restack.json")); !os.IsNotExist(err) {
				t.Fatal("finished recovery left journal")
			}
			w.assertClean(w.Local)
		})
	}
}

func TestCreateAtAnnotatedUpstreamTagCanPreserveAnOlderDetachedHead(t *testing.T) {
	w := newWorld(t)
	old := w.tip(w.Local, "main")
	w.commit(w.Other, "main", "upstream.txt", "upstream")
	w.git(w.Other, "push", "-q", "origin", "main")
	w.git(w.Local, "fetch", "-q", "origin")
	w.git(w.Local, "tag", "-a", "synthetic-upstream", "origin/main", "-m", "synthetic upstream")
	start := w.tip(w.Local, "origin/main")
	w.git(w.Local, "switch", "--detach", old)
	mustRun(t, "create", "synthetic-new", "--parent", "main", "--at", "synthetic-upstream", "--apply")
	if w.tip(w.Local, "synthetic-new") != start {
		t.Fatal("annotated start did not peel to upstream commit")
	}
	if got := w.git(w.Local, "rev-parse", "refs/g2g/forkpoints/synthetic-new"); got != start {
		t.Fatalf("fork=%s, want %s", got, start)
	}
	w.assertClean(w.Local)
}

func TestPullAboveHonorsInheritedDrops(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.commit(w.Local, "synthetic-a", "dropped.txt", "drop")
	mustRun(t, "track", "--parent", "main", "--apply")
	mustRun(t, "push", "--apply")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	mustRun(t, "track", "--parent", "synthetic-a", "--apply")
	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "switch", "-q", "-c", "synthetic-a", "origin/synthetic-a")
	w.git(w.Other, "reset", "--hard", "HEAD~1")
	w.git(w.Other, "push", "-q", "--force", "origin", "synthetic-a")
	published := w.tip(w.Other, "synthetic-a")
	stdout := mustRun(t, "pull", "--above", "synthetic-a", "--apply")
	if !strings.Contains(stdout, "g2g pull --branch synthetic-b --above synthetic-a --keep <commit>") {
		t.Fatalf("drop hint lost boundary:\n%s", stdout)
	}
	if w.tip(w.Local, "synthetic-a") != published || !w.contains(w.Local, "synthetic-a", "synthetic-b") {
		t.Fatal("drop did not follow published boundary")
	}
	if w.git(w.Local, "ls-tree", "--name-only", "synthetic-b", "dropped.txt") != "" {
		t.Fatal("child restored dropped inherited work")
	}
	w.assertClean(w.Local)
}

func TestPullAboveRefusesMissingPublishedOrUnrelatedBoundary(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--parent", "main", "--apply")
	w.branchOff("main", "synthetic-other", "other.txt")
	mustRun(t, "track", "--parent", "main", "--apply")
	w.git(w.Local, "switch", "-q", "synthetic-a")
	for _, above := range []string{"synthetic-a", "synthetic-other"} {
		stdout, _, err := run(t, "pull", "--above", above, "--apply")
		if err == nil {
			t.Fatalf("accepted missing or unrelated boundary %s:\n%s", above, stdout)
		}
	}
}

func TestPullAboveFollowsCleanWorktreeOrExplicitlySkipsIt(t *testing.T) {
	for _, policy := range []string{"follow", "skip"} {
		t.Run(policy, func(t *testing.T) {
			w := newWorld(t)
			w.branchOff("main", "synthetic-a", "a.txt")
			mustRun(t, "track", "--parent", "main", "--apply")
			mustRun(t, "push", "--apply")
			before := w.tip(w.Local, "synthetic-a")
			w.branchOff("synthetic-a", "synthetic-b", "b.txt")
			mustRun(t, "track", "--parent", "synthetic-a", "--apply")
			held := filepath.Join(filepath.Dir(w.Local), "synthetic-held")
			w.git(w.Local, "worktree", "add", "-q", held, "synthetic-a")
			w.git(w.Other, "fetch", "-q", "origin")
			w.git(w.Other, "switch", "-q", "-c", "synthetic-a", "origin/synthetic-a")
			w.commit(w.Other, "synthetic-a", "theirs.txt", "theirs")
			w.git(w.Other, "push", "-q", "origin", "synthetic-a")
			published := w.tip(w.Other, "synthetic-a")
			mustRun(t, "pull", "--above", "synthetic-a", "--worktrees", policy, "--apply")
			want := published
			if policy == "skip" {
				want = before
			}
			if w.tip(w.Local, "synthetic-a") != want || w.syncedTip(w.Local, "synthetic-a") != want {
				t.Fatal("worktree policy or agreement was not preserved")
			}
			if !w.contains(w.Local, "synthetic-a", "synthetic-b") {
				t.Fatal("child is not on the boundary's actual tip")
			}
			w.assertClean(held)
			w.assertClean(w.Local)
		})
	}
}

func TestPullAboveReforksAParentRewrittenOntoAnOlderTrunk(t *testing.T) {
	w := newWorld(t)
	oldBase := w.tip(w.Local, "main")
	w.commit(w.Local, "main", "trunk.txt", "trunk advance")
	w.git(w.Local, "push", "-q", "origin", "main")
	trunk := w.tip(w.Local, "main")
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--parent", "main", "--apply")
	mustRun(t, "push", "--apply")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	mustRun(t, "track", "--parent", "synthetic-a", "--apply")
	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "switch", "-q", "-c", "synthetic-a", "origin/synthetic-a")
	w.git(w.Other, "rebase", "--onto", oldBase, trunk, "synthetic-a")
	w.commit(w.Other, "synthetic-a", "new-a.txt", "publisher's new own work")
	w.git(w.Other, "push", "-q", "--force", "origin", "synthetic-a")
	published := w.tip(w.Other, "synthetic-a")
	mustRun(t, "pull", "--above", "synthetic-a", "--apply")
	if w.tip(w.Local, "synthetic-a") != published || w.tip(w.Local, "main") != trunk {
		t.Fatal("boundary was replayed or trunk was moved")
	}
	if got := w.git(w.Local, "rev-parse", "refs/g2g/forkpoints/synthetic-a"); got != oldBase {
		t.Fatalf("boundary fork %s, want older evidenced trunk base %s", got, oldBase)
	}
	if !w.contains(w.Local, oldBase, "synthetic-a") || !w.contains(w.Local, "synthetic-a", "synthetic-b") {
		t.Fatal("invalid boundary or child ancestry")
	}
	if w.git(w.Local, "rev-list", "--count", "synthetic-a..synthetic-b") != "1" {
		t.Fatal("child replay range included parent work")
	}
	status := mustRun(t, "status")
	if strings.Contains(status, "moved off parent") || strings.Contains(status, "fork point") {
		t.Fatalf("collected boundary left invalid structure:\n%s", status)
	}
	w.assertClean(w.Local)
}

func TestPullAboveRefusesAnAmbiguousRewrittenFeatureParentBoundary(t *testing.T) {
	w := newWorld(t)
	root := w.tip(w.Local, "main")
	w.branchOff("main", "synthetic-parent", "parent.txt")
	mustRun(t, "track", "--parent", "main", "--apply")
	parent := w.tip(w.Local, "synthetic-parent")
	w.branchOff("synthetic-parent", "synthetic-a", "a.txt")
	mustRun(t, "track", "--parent", "synthetic-parent", "--apply")
	mustRun(t, "push", "--apply")
	boundary := w.tip(w.Local, "synthetic-a")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	mustRun(t, "track", "--parent", "synthetic-a", "--apply")
	child := w.tip(w.Local, "synthetic-b")
	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "switch", "-q", "-c", "synthetic-a", "origin/synthetic-a")
	w.git(w.Other, "rebase", "--onto", root, parent, "synthetic-a")
	w.commit(w.Other, "synthetic-a", "new-a.txt", "publisher")
	w.git(w.Other, "push", "-q", "--force", "origin", "synthetic-a")
	stdout, _, err := run(t, "pull", "--above", "synthetic-a", "--apply")
	if err == nil || !strings.Contains(stdout, "own replay range cannot be established") {
		t.Fatalf("ambiguous feature boundary accepted: %v\n%s", err, stdout)
	}
	if w.tip(w.Local, "synthetic-a") != boundary || w.tip(w.Local, "synthetic-b") != child {
		t.Fatal("refused boundary moved before validation")
	}
	w.assertClean(w.Local)
}

func TestPullAboveCollectedOlderForkSurvivesChildConflictAbort(t *testing.T) {
	w := newWorld(t)
	oldBase := w.tip(w.Local, "main")
	w.commit(w.Local, "main", "trunk.txt", "trunk")
	w.git(w.Local, "push", "-q", "origin", "main")
	trunk := w.tip(w.Local, "main")
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--parent", "main", "--apply")
	mustRun(t, "push", "--apply")
	w.git(w.Local, "switch", "-qc", "synthetic-b", "synthetic-a")
	w.commit(w.Local, "synthetic-b", "a.txt", "child conflict")
	mustRun(t, "track", "--parent", "synthetic-a", "--apply")
	originalChild := w.tip(w.Local, "synthetic-b")
	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "switch", "-qc", "synthetic-a", "origin/synthetic-a")
	w.git(w.Other, "rebase", "--onto", oldBase, trunk, "synthetic-a")
	w.commit(w.Other, "synthetic-a", "a.txt", "publisher conflict")
	w.git(w.Other, "push", "-q", "--force", "origin", "synthetic-a")
	published := w.tip(w.Other, "synthetic-a")
	stdout, _, err := run(t, "pull", "--above", "synthetic-a", "--apply")
	if err == nil {
		t.Fatalf("expected child conflict:\n%s", stdout)
	}
	// Collection is complete before replay starts; its fork belongs to the
	// published parent and must not be rolled back with the child's replay.
	if got := w.git(w.Local, "rev-parse", "refs/g2g/forkpoints/synthetic-a"); got != oldBase {
		t.Fatalf("fork was not recorded before conflict: %s", got)
	}
	mustRun(t, "restack", "--abort")
	if w.tip(w.Local, "synthetic-a") != published || w.tip(w.Local, "synthetic-b") != originalChild {
		t.Fatal("abort changed collected parent or failed to restore child")
	}
	if got := w.git(w.Local, "rev-parse", "refs/g2g/forkpoints/synthetic-a"); got != oldBase {
		t.Fatalf("abort undid collected-parent fork: %s", got)
	}
	w.assertClean(w.Local)
}

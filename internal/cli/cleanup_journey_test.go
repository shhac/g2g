package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
)

func TestJourneyCleanupRemovesLandedLocalBranchesAndMissingRecords(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-landed", "one.txt")
	w.commit(w.Local, "synthetic-landed", "two.txt", "two")
	mustRun(t, "track", "--branch", "synthetic-landed", "--parent", "main", "--apply")
	w.branchOff("main", "synthetic-gone", "gone.txt")
	mustRun(t, "track", "--branch", "synthetic-gone", "--parent", "main", "--apply")
	w.branchOff("synthetic-gone", "synthetic-gone-child", "child.txt")
	mustRun(t, "track", "--branch", "synthetic-gone-child", "--parent", "synthetic-gone", "--apply")
	w.branchOff("main", "synthetic-kept", "kept.txt")
	mustRun(t, "track", "--branch", "synthetic-kept", "--parent", "main", "--apply")
	w.git(w.Local, "switch", "-q", "main")
	w.git(w.Local, "merge", "-q", "--squash", "synthetic-landed")
	w.git(w.Local, "commit", "-qm", "synthetic squash")
	w.assertClean(w.Local)
	w.git(w.Local, "branch", "-D", "synthetic-gone-child", "synthetic-gone")
	w.assertClean(w.Local)
	before := mustRun(t, "status", "--scope", "all", "--no-links")
	if strings.Count(before, "g2g untrack --branch synthetic-gone") != 1 || !strings.Contains(before, "g2g untrack --branch synthetic-gone --scope subtree") {
		t.Fatalf("missing-chain hints were not consolidated:\n%s", before)
	}
	preview := mustRun(t, "prune", "--scope", "all", "--delete-branches", "--forget-missing")
	if !strings.Contains(preview, "delete local branch") || !strings.Contains(preview, "synthetic-gone-child") {
		t.Fatalf("preview omits cleanup:\n%s", preview)
	}
	if !w.hasRef("refs/heads/synthetic-landed") {
		t.Fatal("preview deleted a branch")
	}
	mustRun(t, "prune", "--scope", "all", "--delete-branches", "--forget-missing", "--apply")
	w.assertClean(w.Local)
	if w.hasRef("refs/heads/synthetic-landed") || !w.hasRef("refs/heads/synthetic-kept") {
		t.Fatal("cleanup deleted the wrong branches")
	}
	status := mustRun(t, "status", "--scope", "all")
	for _, forgotten := range []string{"synthetic-landed", "synthetic-gone", "synthetic-gone-child"} {
		if strings.Contains(status, forgotten) {
			t.Fatalf("record survived cleanup: %s\n%s", forgotten, status)
		}
	}
}

func observationStore() *githubstack.FileObservations {
	return &githubstack.FileObservations{Git: git.Client{Runner: subprocess.ExecRunner{}}}
}

func TestJourneySubmitRemembersTheNewPRWithoutOnlineStatus(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	recorder := testutil.FakeCLIs(t, map[string][]testutil.Route{"gh": {
		{Prefix: "api graphql", Output: `{"data":{"repository":{"nameWithOwner":"example/synthetic","pr0":{"nodes":[]}}}}`},
		{Prefix: "pr create", Output: "https://example.test/synthetic/repo/pull/41"},
	}})
	spec := filepath.Join(t.TempDir(), "submission.json")
	if err := os.WriteFile(spec, []byte(`{"version":1,"draft":true,"pulls":[{"branch":"synthetic-a","title":"Synthetic change","body":"Synthetic body"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "submit", "--spec", spec, "--no-comment", "--apply")
	w.assertClean(w.Local)
	if local, published := w.tip(w.Local, "synthetic-a"), strings.TrimSpace(w.git(w.Remote, "rev-parse", "synthetic-a")); local != published {
		t.Fatal("submit did not publish its branch")
	}
	seen, err := observationStore().Load(context.Background())
	if err != nil || seen["synthetic-a"].PullRequest.Number != 41 {
		t.Fatalf("submission observation: %+v, %v", seen, err)
	}
	before := len(recorder.Calls())
	out := mustRun(t, "status", "--no-links")
	if !strings.Contains(out, "#41") || !strings.Contains(out, "last seen open") {
		t.Fatalf("offline status forgot submission:\n%s", out)
	}
	if len(recorder.Calls()) != before {
		t.Fatal("offline status invoked gh")
	}
	recorder.Find("gh pr create --head synthetic-a --base main")
}

func TestJourneyLandKeepsConfirmedPRKnowledgeAfterPruning(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	w.git(w.Local, "push", "-q", "origin", "synthetic-a")
	state := landingGitHubStack(t, w.Remote, "synthetic-a")
	mustRun(t, "land", "--no-comment", "--apply")
	w.assertClean(w.Local)
	if readState(t, state, "pr-41.state") != "MERGED" {
		t.Fatal("fake GitHub did not perform the real squash merge")
	}
	if w.hasRef("refs/heads/synthetic-a") {
		t.Fatal("land did not delete the branch")
	}
	seen, err := observationStore().Load(context.Background())
	if err != nil || seen["synthetic-a"].PullRequest.State != "MERGED" || !seen["synthetic-a"].MergeRequestedAt.IsZero() {
		t.Fatalf("confirmed merge was lost after pruning: %+v, %v", seen, err)
	}
	// The content is in the real remote, not merely in a successful report.
	w.git(w.Other, "fetch", "-q", "origin")
	w.assertHas(w.Other, "origin/main", "a.txt")
}

func TestJourneyOnlineStatusInspectsADeletedLocalBranchAndRefreshesOfflineKnowledge(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	w.git(w.Local, "push", "-q", "origin", "synthetic-a")
	state := landingGitHubStack(t, w.Remote, "synthetic-a")
	mustRun(t, "github", "status", "--branch", "synthetic-a")
	w.git(w.Local, "switch", "-q", "main")
	w.git(w.Local, "branch", "-D", "synthetic-a")
	w.assertClean(w.Local)
	before := mustRun(t, "status", "--scope", "all", "--no-links")
	if !strings.Contains(before, "last seen open") {
		t.Fatalf("offline knowledge missing:\n%s", before)
	}
	if err := os.WriteFile(filepath.Join(state, "pr-41.state"), []byte("CLOSED\n"), 0600); err != nil {
		t.Fatal(err)
	}
	out := mustRun(t, "github", "status", "--branch", "synthetic-a", "--no-links")
	if !strings.Contains(out, "local branch missing") || !strings.Contains(out, "closed") || !strings.Contains(out, "#41") || strings.Contains(out, "g2g submit") {
		t.Fatalf("online status of deleted branch:\n%s", out)
	}
	after := mustRun(t, "status", "--scope", "all", "--no-links")
	if !strings.Contains(after, "last seen closed") {
		t.Fatalf("online knowledge not refreshed:\n%s", after)
	}
	w.assertClean(w.Local)
}

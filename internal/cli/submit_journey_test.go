package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Existing-only apply must reach the remote, including a local stack whose
// pull requests intentionally all target the trunk. No PR text is needed.
func TestJourneySubmitExistingPullRequestsPublishesAndPreservesBases(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a", "--apply")
	mustRun(t, "push", "--apply")
	state := landingGitHubPulls(t, w.Remote, [2]string{"synthetic-a", "main"}, [2]string{"synthetic-b", "main"})
	w.commit(w.Local, "synthetic-b", "new.txt", "synthetic new work")
	w.git(w.Local, "switch", "synthetic-b")
	before := w.tip(w.Remote, "synthetic-b")
	out := mustRun(t, "submit", "--no-template", "--no-comment", "--json")
	if !strings.Contains(out, "atomic") || w.tip(w.Remote, "synthetic-b") != before {
		t.Fatalf("preview did not describe publication or moved remote: %s", out)
	}
	if _, _, err := run(t, "submit", "--no-template", "--no-comment", "--link", "--apply"); err == nil {
		t.Fatal("linking with different bases was accepted")
	}
	if w.tip(w.Remote, "synthetic-b") != before {
		t.Fatal("blocked linking published commits")
	}
	mustRun(t, "submit", "--no-template", "--apply", "--json")
	for _, branch := range []string{"synthetic-a", "synthetic-b"} {
		if remote, local := w.tip(w.Remote, branch), w.tip(w.Local, branch); remote != local {
			t.Errorf("%s not published: remote %s, local %s", branch, remote, local)
		}
	}
	for _, number := range []string{"41", "42"} {
		base, err := os.ReadFile(filepath.Join(state, "pr-"+number+".base"))
		if err != nil || strings.TrimSpace(string(base)) != "main" {
			t.Errorf("PR %s base changed: %s, %v", number, base, err)
		}
	}
	calls, err := os.ReadFile(filepath.Join(state, "calls.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(calls), "pr create") || strings.Contains(string(calls), "updatePullRequest(") {
		t.Fatalf("existing PRs were mutated: %s", calls)
	}
	w.assertClean(w.Local)
}

// A branch created to work in next has nothing to submit yet. It used to be
// taken in by a submit of the branch below it, which asked for a title and a
// pull request GitHub refuses for a branch with no commits.
func TestJourneySubmitLeavesOutABranchWithNothingToSubmitYet(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	w.git(w.Local, "switch", "synthetic-a")
	mustRun(t, "create", "synthetic-b", "--apply")
	state := landingGitHubPulls(t, w.Remote, [2]string{"synthetic-a", "main"})

	preview := mustRun(t, "submit", "--branch", "synthetic-a", "--no-template", "--no-comment")
	if !strings.Contains(preview, "left out · no commits of its own yet") {
		t.Errorf("preview does not say synthetic-b is left out:\n%s", preview)
	}
	specDir := t.TempDir()
	mustRun(t, "submit", "--branch", "synthetic-a", "--write-spec", specDir)
	if spec, err := os.ReadFile(filepath.Join(specDir, "submission.json")); err != nil || strings.Contains(string(spec), "synthetic-b") {
		t.Errorf("spec asks about synthetic-b: %s, %v", spec, err)
	}

	mustRun(t, "submit", "--branch", "synthetic-a", "--no-template", "--no-comment", "--apply")

	if remote, local := w.tip(w.Remote, "synthetic-a"), w.tip(w.Local, "synthetic-a"); remote != local {
		t.Errorf("synthetic-a not published: remote %s, local %s", remote, local)
	}
	if published := w.git(w.Remote, "branch", "--list", "synthetic-b"); published != "" {
		t.Errorf("synthetic-b was pushed: %q", published)
	}
	calls, err := os.ReadFile(filepath.Join(state, "calls.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(calls), "pr create") {
		t.Errorf("a pull request was opened: %s", calls)
	}
	w.assertClean(w.Local)
}

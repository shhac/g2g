package cli_test

import (
	"fmt"
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

// A stack that forks above a branch publishes whole: one atomic push, and each
// missing pull request opened on the branch it is recorded on, never on a
// sibling. It used to be refused, so the only way through was a submit per
// leaf, with the stack half published between them.
func TestJourneySubmitAStackThatForks(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	w.branchOff("synthetic-b", "synthetic-c", "c.txt")
	w.branchOff("synthetic-b", "synthetic-d", "d.txt")
	w.branchOff("synthetic-d", "synthetic-e", "e.txt")
	for _, edge := range [][2]string{{"synthetic-a", "main"}, {"synthetic-b", "synthetic-a"}, {"synthetic-c", "synthetic-b"}, {"synthetic-d", "synthetic-b"}, {"synthetic-e", "synthetic-d"}} {
		mustRun(t, "track", "--branch", edge[0], "--parent", edge[1], "--apply")
	}
	mustRun(t, "push", "--branch", "synthetic-b", "--scope", "path", "--apply")
	// A branch just created on one arm has nothing to submit, and is left out
	// of that arm alone.
	w.git(w.Local, "switch", "synthetic-c")
	mustRun(t, "create", "synthetic-f", "--apply")
	state := landingGitHubPulls(t, w.Remote, [2]string{"synthetic-a", "main"}, [2]string{"synthetic-b", "synthetic-a"})
	w.git(w.Local, "switch", "synthetic-a")

	preview := mustRun(t, "submit", "--branch", "synthetic-a", "--scope", "stack", "--no-template", "--no-comment")
	for _, drawn := range []string{"├─", "└─", "synthetic-f", "left out · no commits of its own yet"} {
		if !strings.Contains(preview, drawn) {
			t.Errorf("preview does not draw the fork (%q missing):\n%s", drawn, preview)
		}
	}
	if stdout, stderr, _ := run(t, "submit", "--branch", "synthetic-a", "--scope", "stack", "--no-template", "--link"); !strings.Contains(stdout, "drop --link") {
		t.Errorf("--link on a fork was not refused with a way out:\n%s\n%s", stdout, stderr)
	}

	specDir := t.TempDir()
	mustRun(t, "submit", "--branch", "synthetic-a", "--scope", "stack", "--no-template", "--write-spec", specDir)
	specPath := filepath.Join(specDir, "submission.json")
	spec, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatal(err)
	}
	filled := strings.NewReplacer(`"title": ""`, `"title": "synthetic title"`).Replace(string(spec))
	if err := os.WriteFile(specPath, []byte(filled), 0o600); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "submit", "--branch", "synthetic-a", "--scope", "stack", "--spec", specPath, "--no-comment", "--apply")

	for _, branch := range []string{"synthetic-a", "synthetic-b", "synthetic-c", "synthetic-d", "synthetic-e"} {
		if remote, local := w.tip(w.Remote, branch), w.tip(w.Local, branch); remote != local {
			t.Errorf("%s not published: remote %s, local %s", branch, remote, local)
		}
	}
	if published := w.git(w.Remote, "branch", "--list", "synthetic-f"); published != "" {
		t.Errorf("synthetic-f was pushed: %q", published)
	}
	if _, err := os.Stat(filepath.Join(state, "pr-46.head")); err == nil {
		t.Error("a pull request was opened for synthetic-f")
	}
	bases := map[string]string{}
	for number := 43; number <= 45; number++ {
		head, _ := os.ReadFile(filepath.Join(state, fmt.Sprintf("pr-%d.head", number)))
		base, _ := os.ReadFile(filepath.Join(state, fmt.Sprintf("pr-%d.base", number)))
		bases[strings.TrimSpace(string(head))] = strings.TrimSpace(string(base))
	}
	want := map[string]string{"synthetic-c": "synthetic-b", "synthetic-d": "synthetic-b", "synthetic-e": "synthetic-d"}
	for branch, base := range want {
		if bases[branch] != base {
			t.Errorf("%s opened on %q, want %q (all: %v)", branch, bases[branch], base, bases)
		}
	}
	w.assertClean(w.Local)
}

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
	if strings.Contains(string(calls), "pr create") || strings.Contains(string(calls), "pr edit") {
		t.Fatalf("existing PRs were mutated: %s", calls)
	}
	w.assertClean(w.Local)
}

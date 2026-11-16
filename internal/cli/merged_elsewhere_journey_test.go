package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// GitHub can mark a head merged into a feature base while none of its work
// has reached the trunk. Lifecycle history must not send this stack to pull.
func TestJourneyMergedIntoFeatureBaseStillNeedsReplacement(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-head", "head.txt")
	w.branchOff("synthetic-head", "synthetic-base", "feature.txt")
	mustRun(t, "track", "--branch", "synthetic-head", "--parent", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-base", "--parent", "synthetic-head", "--apply")
	mustRun(t, "push", "--apply")
	state := landingGitHubPulls(t, w.Remote, [2]string{"synthetic-head", "synthetic-base"}, [2]string{"synthetic-base", "synthetic-head"})
	if err := os.WriteFile(filepath.Join(state, "pr-41.state"), []byte("MERGED\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !w.contains(w.Remote, "synthetic-head", "synthetic-base") || w.contains(w.Remote, "synthetic-head", "main") {
		t.Fatal("merged feature work is not exclusively outside the trunk")
	}
	out := mustRun(t, "github", "status", "--branch", "synthetic-head")
	if !strings.Contains(out, "merged into synthetic-base, not landed") || !strings.Contains(out, "g2g submit") || strings.Contains(out, "g2g pull") {
		t.Errorf("GitHub lifecycle was mistaken for landing:\n%s", out)
	}
	preview := mustRun(t, "submit", "--branch", "synthetic-head", "--no-template", "--no-comment")
	if !strings.Contains(preview, "merged into synthetic-base, not landed") {
		t.Errorf("submit hides why a replacement is needed:\n%s", preview)
	}
	w.assertClean(w.Local)
}

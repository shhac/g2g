package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// syntheticLandedParent is a stack of two whose parent was squash-merged into
// the remote's trunk and then pulled: the trunk has advanced, the parent is
// collapsed onto it and the child replayed, and nothing has been pruned. That
// is the state pull leaves, with its suggestion to prune not yet followed.
// The child has a pull request of its own only when childOpen.
func syntheticLandedParent(t *testing.T, childOpen bool) (*world, string) {
	t.Helper()
	w := newWorld(t)
	w.branchOff("main", "synthetic-parent", "parent.txt")
	w.branchOff("synthetic-parent", "synthetic-child", "child.txt")
	mustRun(t, "track", "--branch", "synthetic-parent", "--parent", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-child", "--parent", "synthetic-parent", "--apply")
	mustRun(t, "push", "--apply")
	pulls := [][2]string{{"synthetic-parent", "main"}}
	if childOpen {
		pulls = append(pulls, [2]string{"synthetic-child", "synthetic-parent"})
	}
	state := landingGitHubPulls(t, w.Remote, pulls...)
	runIn(t, w.Local, "gh", "pr", "merge", "41", "--squash", "--match-head-commit", w.tip(w.Remote, "synthetic-parent"))
	mustRun(t, "pull", "--branch", "synthetic-child", "--apply")
	w.assertClean(w.Local)
	if !w.contains(w.Local, "main", "synthetic-child") || w.tip(w.Local, "synthetic-parent") != w.tip(w.Local, "main") {
		t.Fatal("the pull did not collapse the landed parent and replay the child onto the trunk")
	}
	return w, state
}

// A parent whose pull request merged and whose work the trunk holds has
// nothing to submit. Submit used to offer to open a new pull request for it,
// and blocked the child above on a title for work already in the trunk.
func TestJourneySubmitLeavesOutAParentLandedInTheTrunk(t *testing.T) {
	for _, scope := range []string{"path", "stack"} {
		t.Run(scope, func(t *testing.T) {
			w, state := syntheticLandedParent(t, true)
			parent := w.tip(w.Remote, "synthetic-parent")
			args := []string{"submit", "--branch", "synthetic-child", "--scope", scope, "--no-template", "--no-comment"}
			preview := mustRun(t, args...)
			if strings.Contains(preview, "create ") || strings.Contains(preview, "submission spec") || strings.Contains(preview, "Apply blocked") {
				t.Errorf("submit offers a pull request for a branch already in the trunk:\n%s", preview)
			}
			for _, want := range []string{"left out · merged · landed in main", "synthetic-parent has landed in main and is left out", "g2g prune --branch synthetic-child", "#42 (synthetic-child) is still based on synthetic-parent"} {
				if !strings.Contains(preview, want) {
					t.Errorf("preview lacks %q:\n%s", want, preview)
				}
			}
			specDir := t.TempDir()
			mustRun(t, append(args, "--write-spec", specDir)...)
			if spec, err := os.ReadFile(filepath.Join(specDir, "submission.json")); err != nil || strings.Contains(string(spec), "synthetic-parent") {
				t.Errorf("spec asks for the landed parent: %s, %v", spec, err)
			}
			if t.Failed() {
				return
			}
			mustRun(t, append(args, "--apply")...)
			w.assertClean(w.Local)
			if w.tip(w.Remote, "synthetic-parent") != parent {
				t.Error("submit pushed the landed parent")
			}
			if w.tip(w.Remote, "synthetic-child") != w.tip(w.Local, "synthetic-child") {
				t.Error("submit did not publish the child")
			}
			if calls := readState(t, state, "calls.log"); strings.Contains(calls, "pr create") {
				t.Errorf("submit opened a pull request:\n%s", calls)
			}
			if base := readState(t, state, "pr-42.base"); base != "synthetic-parent" {
				t.Errorf("submit moved the child's existing base to %q", base)
			}

			// The cleanup the preview names, in the order it names it.
			mustRun(t, "prune", "--branch", "synthetic-child", "--apply")
			structure := w.readStructure()
			if _, tracked := structure["synthetic-parent"]; tracked || structure["synthetic-child"] != "main" {
				t.Fatalf("the suggested prune left %v", structure)
			}
			mustRun(t, "github", "retarget", "--branch", "synthetic-child", "--apply")
			if base := readState(t, state, "pr-42.base"); base != "main" {
				t.Errorf("the suggested retarget left the child based on %q", base)
			}
			after := mustRun(t, args...)
			if strings.Contains(after, "synthetic-parent") || strings.Contains(after, "Apply blocked") {
				t.Errorf("the cleaned-up stack still mentions the landed parent:\n%s", after)
			}
			w.assertClean(w.Local)
		})
	}
}

// The record still names the landed parent, so a pull request opened for the
// child now would be based on a finished branch. Submit refuses before
// publishing, and the cleanup it names is what makes the trunk the base.
func TestJourneySubmitRefusesToOpenAPullRequestOnALandedParent(t *testing.T) {
	w, state := syntheticLandedParent(t, false)
	before := w.git(w.Remote, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads/")
	args := []string{"submit", "--branch", "synthetic-child", "--no-comment"}
	preview := mustRun(t, append(args, "--no-template", "--json")...)
	var result struct {
		Repair struct {
			Reason string `json:"reason"`
			Ways   []struct {
				Command string `json:"command"`
			} `json:"ways"`
		} `json:"repair"`
	}
	if err := json.Unmarshal([]byte(preview), &result); err != nil {
		t.Fatal(err)
	}
	if result.Repair.Reason != "synthetic-child depends on landed base synthetic-parent" || len(result.Repair.Ways) != 1 {
		t.Fatalf("creation across a landed base was not refused with its cleanup:\n%s", preview)
	}
	spec := filepath.Join(t.TempDir(), "synthetic-submission.json")
	if err := os.WriteFile(spec, []byte(`{"version":1,"draft":true,"pulls":[{"branch":"synthetic-child","title":"synthetic child"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, append(args, "--spec", spec, "--apply")...); err == nil || !strings.Contains(err.Error(), "depends on landed base") {
		t.Fatalf("apply did not keep the refusal: %v", err)
	}
	if w.git(w.Remote, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads/") != before {
		t.Error("the refused submit published refs")
	}
	if calls := readState(t, state, "calls.log"); strings.Contains(calls, "pr create") {
		t.Errorf("the refused submit opened a pull request:\n%s", calls)
	}

	mustRun(t, append(strings.Fields(result.Repair.Ways[0].Command)[1:], "--apply")...)
	if structure := w.readStructure(); structure["synthetic-child"] != "main" {
		t.Fatalf("the named cleanup left %v", structure)
	}
	mustRun(t, append(args, "--spec", spec, "--apply")...)
	if calls := readState(t, state, "calls.log"); !strings.Contains(calls, "--head synthetic-child") || strings.Contains(calls, "--head synthetic-parent") {
		t.Errorf("the child's pull request was not the only one opened:\n%s", calls)
	}
	if base := readState(t, state, "pr-42.base"); base != "main" {
		t.Errorf("the child's pull request opened on %q", base)
	}
	if w.tip(w.Remote, "synthetic-child") != w.tip(w.Local, "synthetic-child") {
		t.Error("submit did not publish the child")
	}
	w.assertClean(w.Local)
}

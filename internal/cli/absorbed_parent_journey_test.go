package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJourneySubmitLeavesOutAChildSquashedIntoItsParent(t *testing.T) {
	for _, knowledge := range []string{"local-parent", "known-remote-parent", "new-child-work"} {
		t.Run(knowledge, func(t *testing.T) {
			w := newWorld(t)
			w.git(w.Local, "branch", "-m", "main", "synthetic-main")
			w.git(w.Local, "push", "-qu", "origin", "synthetic-main")
			w.branchOff("synthetic-main", "synthetic-parent", "parent.txt")
			w.branchOff("synthetic-parent", "synthetic-child", "child.txt")
			w.commit(w.Local, "synthetic-child", "second.txt", "synthetic second child commit")
			mustRun(t, "track", "--branch", "synthetic-parent", "--parent", "synthetic-main", "--apply")
			mustRun(t, "track", "--branch", "synthetic-child", "--parent", "synthetic-parent", "--apply")
			mustRun(t, "push", "--apply")
			state := landingGitHubPulls(t, w.Remote, [2]string{"synthetic-parent", "synthetic-main"}, [2]string{"synthetic-child", "synthetic-parent"})
			child := w.tip(w.Remote, "synthetic-child")
			w.git(w.Local, "switch", "-q", "synthetic-parent")
			runIn(t, w.Local, "gh", "pr", "merge", "42", "--squash", "--match-head-commit", child)
			w.git(w.Local, "fetch", "-q", "--refmap=", "--no-write-fetch-head", "origin", "+refs/heads/synthetic-parent:refs/g2g/remotes/origin/synthetic-parent")
			if knowledge != "known-remote-parent" {
				w.git(w.Local, "merge", "--ff-only", "refs/g2g/remotes/origin/synthetic-parent")
			}
			w.assertClean(w.Local)
			if knowledge == "new-child-work" {
				w.commit(w.Local, "synthetic-child", "followup.txt", "synthetic unabsorbed followup")
				preview := mustRun(t, "submit", "--branch", "synthetic-child", "--no-template", "--no-comment")
				if !strings.Contains(preview, "create draft") || !strings.Contains(preview, "merged into synthetic-parent, not landed") {
					t.Errorf("unabsorbed work lost replacement advice:\n%s", preview)
				}
				status := mustRun(t, "github", "status", "--branch", "synthetic-child")
				if !strings.Contains(status, "not landed") || !strings.Contains(status, "g2g submit") {
					t.Errorf("unabsorbed status lost replacement advice:\n%s", status)
				}
				return
			}
			args := []string{"submit", "--branch", "synthetic-child", "--scope", "stack", "--no-template", "--no-comment"}
			preview := mustRun(t, args...)
			if !strings.Contains(preview, "merged into synthetic-parent; absorbed") || strings.Contains(preview, "create draft") || strings.Contains(preview, "submission spec") {
				t.Errorf("submit offers to recreate the absorbed child:\n%s", preview)
			}
			status := mustRun(t, "github", "status", "--branch", "synthetic-child")
			if !strings.Contains(status, "merged into synthetic-parent; absorbed") || !strings.Contains(status, "g2g prune") || strings.Contains(status, "g2g submit") {
				t.Errorf("github status does not recognise parent absorption:\n%s", status)
			}
			specDir := t.TempDir()
			mustRun(t, append(args, "--write-spec", specDir)...)
			spec, err := os.ReadFile(filepath.Join(specDir, "submission.json"))
			if err != nil || strings.Contains(string(spec), "synthetic-child") {
				t.Errorf("spec includes the absorbed child: %s, %v", spec, err)
			}
			// The stale parent must be collected before publishing it; the
			// child can already be excluded using its known remote copy.
			if knowledge == "known-remote-parent" {
				if !strings.Contains(status, "--above synthetic-parent") {
					t.Errorf("known parent sent straight to prune:\n%s", status)
				}
				mustRun(t, "pull", "--branch", "synthetic-child", "--above", "synthetic-parent", "--apply")
				w.assertClean(w.Local)
			}
			if t.Failed() {
				return
			}
			mustRun(t, append(args, "--apply")...)
			if w.tip(w.Remote, "synthetic-child") != child {
				t.Error("submit pushed the absorbed child")
			}
			calls := readState(t, state, "calls.log")
			if strings.Contains(calls, "pr create") {
				t.Error("submit opened a replacement PR")
			}
			mustRun(t, "prune", "--branch", "synthetic-child", "--scope", "branch", "--apply")
			if _, tracked := w.readStructure()["synthetic-child"]; tracked {
				t.Error("the suggested prune did not forget the child")
			}
			w.assertClean(w.Local)
		})
	}
}

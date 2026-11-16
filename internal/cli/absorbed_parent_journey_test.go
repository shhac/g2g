package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func syntheticAbsorbedInterior(t *testing.T, live bool) (*world, string, string) {
	t.Helper()
	w := newWorld(t)
	w.git(w.Local, "branch", "-m", "main", "synthetic-main")
	w.git(w.Local, "push", "-qu", "origin", "synthetic-main")
	w.branchOff("synthetic-main", "synthetic-parent", "parent.txt")
	w.branchOff("synthetic-parent", "synthetic-child", "child.txt")
	w.commit(w.Local, "synthetic-child", "second.txt", "synthetic second")
	mustRun(t, "track", "--branch", "synthetic-parent", "--parent", "synthetic-main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-child", "--parent", "synthetic-parent", "--apply")
	if live {
		w.branchOff("synthetic-child", "synthetic-top", "top.txt")
		mustRun(t, "track", "--branch", "synthetic-top", "--parent", "synthetic-child", "--apply")
	}
	mustRun(t, "push", "--apply")
	before := w.tip(w.Local, "synthetic-parent")
	state := landingGitHubPulls(t, w.Remote, [2]string{"synthetic-parent", "synthetic-main"}, [2]string{"synthetic-child", "synthetic-parent"})
	w.git(w.Local, "switch", "-q", "synthetic-parent")
	runIn(t, w.Local, "gh", "pr", "merge", "42", "--squash", "--match-head-commit", w.tip(w.Remote, "synthetic-child"))
	w.git(w.Local, "fetch", "-q", "origin")
	w.git(w.Local, "merge", "--ff-only", "origin/synthetic-parent")
	w.assertClean(w.Local)
	return w, state, before
}

func TestJourneyAbsorbedInteriorCleanupAdviceSucceedsInOrder(t *testing.T) {
	w, _, _ := syntheticAbsorbedInterior(t, true)
	preview := mustRun(t, "submit", "--branch", "synthetic-top", "--no-template", "--no-comment", "--json")
	var result struct {
		Repair struct {
			Ways []struct {
				Command string `json:"command"`
			} `json:"ways"`
		} `json:"repair"`
	}
	if err := json.Unmarshal([]byte(preview), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Repair.Ways) < 2 || !strings.HasPrefix(result.Repair.Ways[0].Command, "g2g restack") {
		t.Fatalf("cleanup cannot reconcile descendants before prune: %s", preview)
	}
	for _, way := range result.Repair.Ways {
		if way.Command == "" {
			continue
		}
		args := strings.Fields(way.Command)[1:]
		mustRun(t, args...)
		mustRun(t, append(args, "--apply")...)
		w.assertClean(w.Local)
	}
	if _, tracked := w.readStructure()["synthetic-child"]; tracked {
		t.Fatal("absorbed child still recorded")
	}
	if !w.contains(w.Local, "synthetic-parent", "synthetic-top") {
		t.Fatal("surviving descendant not reconciled")
	}
}

func TestJourneySubmitRefusesAnAbsorbedInteriorBase(t *testing.T) {
	for _, linking := range []bool{false, true} {
		t.Run(map[bool]string{false: "creation", true: "linking"}[linking], func(t *testing.T) {
			w, state, _ := syntheticAbsorbedInterior(t, true)
			w.git(w.Remote, "update-ref", "-d", "refs/heads/synthetic-child")
			if linking {
				for key, value := range map[string]string{"head": "synthetic-top", "base": "synthetic-child"} {
					if err := os.WriteFile(filepath.Join(state, "pr-43."+key), []byte(value+"\n"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			args := []string{"submit", "--branch", "synthetic-top", "--no-template", "--no-comment", "--json"}
			if linking {
				args = append(args, "--link")
			}
			out := mustRun(t, args...)
			if !strings.Contains(out, "depends on absorbed base synthetic-child") {
				t.Fatalf("disconnected submission was not refused before publishing:\n%s", out)
			}
			before := w.git(w.Remote, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads/")
			spec := filepath.Join(t.TempDir(), "synthetic-submission.json")
			if err := os.WriteFile(spec, []byte(`{"version":1,"draft":true,"pulls":[{"branch":"synthetic-top","title":"synthetic descendant"}]}`), 0o600); err != nil {
				t.Fatal(err)
			}
			trace := filepath.Join(t.TempDir(), "synthetic-trace")
			t.Setenv("GIT_TRACE", trace)
			applyArgs := slices.DeleteFunc(slices.Clone(args), func(arg string) bool { return arg == "--no-template" })
			if _, _, err := run(t, append(applyArgs, "--spec", spec, "--apply")...); err == nil || !strings.Contains(err.Error(), "depends on absorbed base") {
				t.Fatalf("apply did not preserve refusal: %v", err)
			}
			log, err := os.ReadFile(trace)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(log), "git push ") || w.git(w.Remote, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads/") != before {
				t.Fatal("refused submit published refs")
			}
			calls := readState(t, state, "calls.log")
			if strings.Contains(calls, "pr create") || strings.Contains(calls, "stack link") {
				t.Fatal("refused submit created or linked PRs")
			}
			w.assertClean(w.Local)
		})
	}
}

func TestJourneySubmitRetainsChildWhenPublicationDropsItsSquash(t *testing.T) {
	w, _, before := syntheticAbsorbedInterior(t, false)
	mustRun(t, "push", "--branch", "synthetic-parent", "--scope", "branch")
	w.git(w.Local, "reset", "--hard", before)
	w.commit(w.Local, "synthetic-parent", "new-parent.txt", "synthetic replacement parent work")
	preview := mustRun(t, "submit", "--branch", "synthetic-child", "--no-template", "--no-comment")
	if strings.Contains(preview, "left out · merged into synthetic-parent; absorbed") || !strings.Contains(preview, "create draft") {
		t.Fatalf("child omitted using content publication will remove:\n%s", preview)
	}
	w.assertClean(w.Local)
}

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
			status := mustRun(t, "github", "status", "--branch", "synthetic-child")
			if !strings.Contains(status, "merged into synthetic-parent; absorbed") || !strings.Contains(status, "g2g prune") || strings.Contains(status, "g2g submit") {
				t.Errorf("github status does not recognise parent absorption:\n%s", status)
			}
			// Both submit scopes include the parent. Collect its squash
			// before publication can use that content to omit the child.
			if knowledge == "known-remote-parent" {
				preview := mustRun(t, args...)
				if !strings.Contains(preview, "the published version differs on synthetic-parent") || !strings.Contains(preview, "create draft") {
					t.Errorf("stale parent publication incorrectly omitted the child:\n%s", preview)
				}
				if !strings.Contains(status, "--above synthetic-parent") {
					t.Errorf("known parent sent straight to prune:\n%s", status)
				}
				mustRun(t, "pull", "--branch", "synthetic-child", "--above", "synthetic-parent", "--apply")
				w.assertClean(w.Local)
			}
			preview := mustRun(t, args...)
			if !strings.Contains(preview, "merged into synthetic-parent; absorbed") || strings.Contains(preview, "create draft") || strings.Contains(preview, "submission spec") {
				t.Errorf("submit offers to recreate the absorbed child:\n%s", preview)
			}
			specDir := t.TempDir()
			mustRun(t, append(args, "--write-spec", specDir)...)
			spec, err := os.ReadFile(filepath.Join(specDir, "submission.json"))
			if err != nil || strings.Contains(string(spec), "synthetic-child") {
				t.Errorf("spec includes the absorbed child: %s, %v", spec, err)
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

package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --onto moves the selection root onto the remote's trunk. A descendant held
// by another worktree keeps its recorded parent and replay range, so leaving
// it does not prevent the branches below it from moving.
func TestJourneyRestackOntoLeavesAHeldDescendant(t *testing.T) {
	for _, test := range []struct {
		name, policy, held string
		dirty              bool
	}{
		{"skip dirty top", "skip", "synthetic-top", true},
		{"follow dirty top", "follow", "synthetic-top", true},
		{"skip clean top", "skip", "synthetic-top", false},
		{"skip middle", "skip", "synthetic-b", true},
		{"follow middle", "follow", "synthetic-b", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := newWorld(t)
			w.git(w.Local, "branch", "synthetic-trunk", "main")
			parent := "synthetic-trunk"
			for _, branch := range []string{"synthetic-a", "synthetic-b", "synthetic-top"} {
				w.branchOff(parent, branch, branch+".txt")
				mustRun(t, "track", "--parent", parent, "--apply")
				w.assertClean(w.Local)
				parent = branch
			}
			w.git(w.Local, "switch", "-q", "synthetic-a")
			elsewhere := filepath.Join(t.TempDir(), "synthetic-holder")
			w.git(w.Local, "worktree", "add", "-q", elsewhere, test.held)
			const progress = "synthetic work in progress\n"
			if test.dirty {
				if err := os.WriteFile(filepath.Join(elsewhere, test.held+".txt"), []byte(progress), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			w.git(w.Other, "switch", "-qc", "synthetic-trunk", "main")
			w.commit(w.Other, "synthetic-trunk", "synthetic-upstream.txt", "synthetic upstream")
			w.git(w.Other, "push", "-q", "origin", "synthetic-trunk")
			w.git(w.Local, "fetch", "-q", "origin")
			upstream := w.tip(w.Local, "origin/synthetic-trunk")
			trunk := w.tip(w.Local, "synthetic-trunk")
			before, top := w.tip(w.Local, test.held), w.tip(w.Local, "synthetic-top")
			fork := w.tip(w.Local, "refs/g2g/forkpoints/"+test.held)
			structure := w.readStore()
			status := w.git(elsewhere, "status", "--porcelain")
			if ordinary := mustRun(t, "restack", "--scope", "stack", "--worktrees", test.policy); !strings.Contains(ordinary, "Nothing to replay") {
				t.Fatalf("without --onto the stack should be aligned:\n%s", ordinary)
			}

			args := []string{"restack", "--scope", "stack", "--onto", "origin/synthetic-trunk", "--worktrees", test.policy}
			preview := mustRun(t, args...)
			if strings.Contains(preview, "Apply blocked") {
				t.Fatalf("--onto refused a held descendant under %s:\n%s", test.policy, preview)
			}
			if !strings.Contains(preview, "Leaves "+test.held+" where it is") || !strings.Contains(preview, "g2g restack") || !strings.Contains(preview, elsewhere) {
				t.Errorf("preview does not name the left branch and how to finish it:\n%s", preview)
			}
			if w.tip(w.Local, test.held) != before || w.readStore() != structure {
				t.Fatal("preview changed the held ref or graph")
			}
			mustRun(t, append(args, "--apply")...)
			w.assertClean(w.Local)
			if !w.contains(w.Local, upstream, "synthetic-a") {
				t.Error("the selection root was not replayed onto the remote trunk")
			}
			if test.held == "synthetic-top" && !w.contains(w.Local, upstream, "synthetic-b") {
				t.Error("the branch below the held descendant was not replayed")
			}
			if w.tip(w.Local, test.held) != before || w.tip(w.Local, "synthetic-top") != top || w.tip(w.Local, "synthetic-trunk") != trunk {
				t.Error("the held branch, its aligned child, or the local trunk moved")
			}
			if got := w.tip(w.Local, "refs/g2g/forkpoints/"+test.held); got != fork {
				t.Errorf("left branch fork = %s, want unchanged %s", got, fork)
			}
			for branch, want := range map[string]string{"synthetic-a": "synthetic-trunk", "synthetic-b": "synthetic-a", "synthetic-top": "synthetic-b"} {
				if got := w.readStructure()[branch]; got != want {
					t.Errorf("%s parent = %q, want %q", branch, got, want)
				}
			}
			if got := w.git(elsewhere, "status", "--porcelain"); got != status {
				t.Errorf("holder status = %q, want unchanged %q", got, status)
			}
			if test.dirty {
				if got, err := os.ReadFile(filepath.Join(elsewhere, test.held+".txt")); err != nil || string(got) != progress {
					t.Fatalf("work in progress changed: %q, %v", got, err)
				}
				w.commit(elsewhere, test.held, test.held+".txt", "synthetic finished work")
			}
			w.assertClean(elsewhere)
			t.Chdir(elsewhere)
			mustRun(t, "restack", "--apply")
			w.assertClean(elsewhere)
			w.assertClean(w.Local)
			if !w.contains(w.Local, upstream, "synthetic-top") {
				t.Error("the later restack did not bring the held branch and its child along")
			}
		})
	}
}

package cli_test

import (
	"strings"
	"testing"
)

func TestJourneyRestackPreviewOnlyPreservesPublicationSafety(t *testing.T) {
	for _, change := range []string{"reviewer-addition", "upstream-drop"} {
		t.Run(change, func(t *testing.T) {
			w := newWorld(t)
			w.git(w.Local, "branch", "-m", "main", "synthetic-main")
			w.git(w.Local, "push", "-qu", "origin", "synthetic-main")
			w.branchOff("synthetic-main", "synthetic-top", "first.txt")
			first := w.tip(w.Local, "synthetic-top")
			w.commit(w.Local, "synthetic-top", "second.txt", "synthetic second")
			mustRun(t, "track", "--branch", "synthetic-top", "--parent", "synthetic-main", "--apply")
			// Plain Git publication has not recorded an agreement in g2g.
			w.git(w.Local, "push", "-q", "origin", "synthetic-top")
			w.git(w.Local, "fetch", "-q", "origin")
			if got := w.git(w.Local, "for-each-ref", "--format=%(refname)", "refs/g2g/synced/"); got != "" {
				t.Fatalf("fixture already has agreements: %s", got)
			}
			w.commit(w.Local, "synthetic-main", "trunk.txt", "synthetic local trunk advance")
			w.git(w.Local, "switch", "-q", "synthetic-top")
			tips := w.git(w.Local, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads/")
			remote := w.tip(w.Remote, "synthetic-top")
			structure := w.readStore()
			mustRun(t, "restack", "--branch", "synthetic-top")
			if got := w.git(w.Local, "for-each-ref", "--format=%(objectname)", "refs/g2g/synced/origin/synthetic-top"); got != remote {
				t.Fatalf("preview agreement = %s, want observed %s", got, remote)
			}
			if got := w.git(w.Local, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads/"); got != tips || w.readStore() != structure || w.tip(w.Remote, "synthetic-top") != remote {
				t.Fatal("preview moved tips or structure")
			}
			w.assertClean(w.Local)
			w.git(w.Other, "fetch", "-q", "origin")
			w.git(w.Other, "switch", "-qc", "synthetic-top", "origin/synthetic-top")
			if change == "reviewer-addition" {
				w.commit(w.Other, "synthetic-top", "reviewer.txt", "synthetic reviewer addition")
			} else {
				w.git(w.Other, "reset", "--hard", first)
			}
			w.git(w.Other, "push", "-q", "--force-with-lease", "origin", "synthetic-top")
			published := w.tip(w.Remote, "synthetic-top")
			w.git(w.Local, "fetch", "-q", "origin")
			preview := mustRun(t, "push", "--branch", "synthetic-top")
			if change == "reviewer-addition" {
				if !strings.Contains(preview, "the published version differs") || !strings.Contains(preview, "synthetic reviewer.txt") {
					t.Fatalf("reviewer commit unprotected or unnamed: %s", preview)
				}
			} else if !strings.Contains(preview, "would put back 1 commit the remote dropped") || !strings.Contains(preview, remote[:12]) {
				t.Fatalf("drop misclassified after abandoned preview: %s", preview)
			}
			if _, _, err := run(t, "push", "--branch", "synthetic-top", "--apply"); err == nil {
				t.Fatal("unsafe publication accepted")
			}
			if w.tip(w.Remote, "synthetic-top") != published || w.readStore() != structure || w.git(w.Local, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads/") != tips {
				t.Fatal("refused push moved tips or structure")
			}
			if got := w.git(w.Local, "for-each-ref", "--format=%(refname)", "refs/g2g/synced/origin/synthetic-main"); got != "" {
				t.Fatalf("trunk received agreement: %s", got)
			}
			if got := w.git(w.Local, "for-each-ref", "--format=%(objectname)", "refs/g2g/synced/origin/synthetic-top"); got != remote {
				t.Fatal("later refusal replaced the preview's observed agreement")
			}
			w.assertClean(w.Local)
		})
	}
}

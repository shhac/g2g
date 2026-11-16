package cli_test

import (
	"strings"
	"testing"
)

func TestJourneySubmitRejectsBranchScope(t *testing.T) {
	w := newWorld(t)
	w.git(w.Local, "branch", "-m", "main", "synthetic-main")
	w.branchOff("synthetic-main", "synthetic-parent", "parent.txt")
	w.branchOff("synthetic-parent", "synthetic-child", "child.txt")
	mustRun(t, "track", "--branch", "synthetic-parent", "--parent", "synthetic-main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-child", "--parent", "synthetic-parent", "--apply")
	mustRun(t, "push", "--apply")
	landingGitHubPulls(t, w.Remote, [2]string{"synthetic-parent", "synthetic-main"}, [2]string{"synthetic-child", "synthetic-parent"})

	for _, apply := range []bool{false, true} {
		args := []string{"submit", "--branch", "synthetic-child", "--scope", "branch", "--no-template", "--no-comment"}
		if apply {
			args = append(args, "--apply")
		}
		_, _, err := run(t, args...)
		if err == nil || !strings.Contains(err.Error(), `unsupported scope "branch" (want stack, path)`) {
			t.Fatalf("submit apply=%t accepted branch scope or failed for another reason: %v", apply, err)
		}
	}
	w.assertClean(w.Local)
}

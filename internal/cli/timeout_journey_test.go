package cli_test

import (
	"strings"
	"testing"
)

// A budget that runs out is reported as that, whatever the call it cut off
// came back saying. The deadline here has passed before the first Git call,
// so every one of them fails, and each command used to give that failure the
// meaning its own code gives a failed read: a branch that is "not a commit in
// this repository", or a bare "context deadline exceeded" with nothing about
// which ceiling or how to raise it.
func TestJourneyARunOutBudgetIsReportedAsATimeout(t *testing.T) {
	commands := map[string][]string{
		"status":          {"status"},
		"doctor":          {"doctor"},
		"github status":   {"github", "status"},
		"restack preview": {"restack", "--branch", "synthetic-b"},
		"restack apply":   {"restack", "--branch", "synthetic-b", "--apply"},
		"pull preview":    {"pull", "--branch", "synthetic-b"},
		"push preview":    {"push", "--branch", "synthetic-b"},
		"submit preview":  {"submit", "--branch", "synthetic-b"},
		"prune preview":   {"prune", "--branch", "synthetic-b"},
		"up":              {"up"},
	}
	for name, args := range commands {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			w.branchOff("main", "synthetic-a", "a.txt")
			w.branchOff("synthetic-a", "synthetic-b", "b.txt")
			mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
			mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a", "--apply")
			w.git(w.Local, "switch", "-q", "synthetic-a")
			w.fakeGitHub("")
			tips := w.git(w.Local, "for-each-ref", "refs/heads")

			stdout, stderr, err := run(t, append(args, "--timeout", "1ns")...)
			if err == nil {
				t.Fatalf("a command with no time to run succeeded:\n%s%s", stdout, stderr)
			}
			said := err.Error() + stdout + stderr
			for _, want := range []string{"timed out working out what to do", "1ns --timeout"} {
				if !strings.Contains(said, want) {
					t.Errorf("missing %q in:\n%s", want, said)
				}
			}
			for _, misleading := range []string{"is not a commit", "context deadline exceeded"} {
				if strings.Contains(said, misleading) {
					t.Errorf("a timeout was reported as %q:\n%s", misleading, said)
				}
			}
			if w.git(w.Local, "for-each-ref", "refs/heads") != tips {
				t.Error("a command that timed out planning moved a branch")
			}
		})
	}
}

package cli

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

type stubConflicts struct {
	paths []string
	err   error
}

func (s stubConflicts) Conflicted(context.Context) ([]string, error) { return s.paths, s.err }

func stoppedOutput(t *testing.T, conflicts conflictReporter, cause error) string {
	t.Helper()
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := stopped(cmd, context.Background(), conflicts, cause, Presentation{}); err != nil {
		t.Fatalf("stopped() error = %v", err)
	}
	return out.String()
}

// A rewrite that stops with nothing unmerged has not hit a conflict, and saying
// it has sends the reader looking for a file that is not there. This is the
// branch that reports the honest thing instead, on the one operation in this
// tool that rewrites history.
func TestStoppedWithNothingUnmergedDoesNotClaimAConflict(t *testing.T) {
	got := stoppedOutput(t, stubConflicts{}, nil)

	if !strings.Contains(got, "with nothing left unmerged") {
		t.Errorf("missing the no-conflict wording:\n%s", got)
	}
	if strings.Contains(got, "Stopped on a conflict") || strings.Contains(got, "Resolve those files") {
		t.Errorf("claimed a conflict when nothing was unmerged:\n%s", got)
	}
	// The remedy has to be reachable from here: this is a half-finished rewrite.
	for _, want := range []string{"git status", "g2g restack --continue", "g2g restack --abort"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in the recovery advice:\n%s", want, got)
		}
	}
}

// What Git said is the only useful thing we have in that case, so it must not
// be swallowed.
func TestStoppedWithNothingUnmergedRelaysTheUnderlyingCause(t *testing.T) {
	got := stoppedOutput(t, stubConflicts{}, fmt.Errorf("synthetic rewrite failure"))

	if !strings.Contains(got, "synthetic rewrite failure") {
		t.Errorf("dropped the cause:\n%s", got)
	}
}

// A failed lookup is not evidence of a clean tree. It takes the same branch,
// because the alternative is naming files it could not read.
func TestStoppedTreatsAnUnreadableIndexAsNoConflictReport(t *testing.T) {
	got := stoppedOutput(t, stubConflicts{err: fmt.Errorf("synthetic index failure")}, nil)

	if !strings.Contains(got, "with nothing left unmerged") {
		t.Errorf("an unreadable index should take the no-conflict branch:\n%s", got)
	}
}

// The conflict branch still names the files, so the two remain distinguishable.
func TestStoppedOnAConflictNamesTheFiles(t *testing.T) {
	got := stoppedOutput(t, stubConflicts{paths: []string{"synthetic/one.go", "synthetic/two.go"}}, nil)

	for _, want := range []string{"Stopped on a conflict", "synthetic/one.go", "synthetic/two.go", "g2g restack --continue"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "nothing left unmerged") {
		t.Errorf("conflict path used the no-conflict wording:\n%s", got)
	}
}

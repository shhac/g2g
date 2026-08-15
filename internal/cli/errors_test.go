package cli

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/githubstack"
)

// exitError builds a real *exec.ExitError with the requested status so the
// remediation lookup is exercised through the same type production code sees.
func exitError(t *testing.T, code int) error {
	t.Helper()
	err := exec.Command("sh", "-c", fmt.Sprintf("exit %d", code)).Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != code {
		t.Fatalf("build exit error %d: %v", code, err)
	}
	return err
}

func TestWriteErrorSurfacesCommandDiagnostic(t *testing.T) {
	err := &githubstack.CommandError{
		Command: "gh repo view",
		Cause:   exitError(t, 4),
		Output:  "gh auth login required. To authenticate, run: gh auth login",
	}

	var out strings.Builder
	writeError(&out, err)
	output := out.String()

	for _, want := range []string{
		"error: gh repo view failed:",
		"  gh auth login required. To authenticate, run: gh auth login",
		"GitHub CLI authentication is required. Run: gh auth login",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("writeError output missing %q:\n%s", want, output)
		}
	}
}

func TestWriteErrorReportsMissingStackSubcommand(t *testing.T) {
	err := &githubstack.CommandError{
		Command: "gh stack link --base synthetic-main synthetic-a synthetic-b",
		Cause:   exitError(t, 1),
		Output:  "unknown command \"stack\" for \"gh\"",
	}

	var out strings.Builder
	writeError(&out, err)

	if got := out.String(); !strings.Contains(got, "This gh build has no `stack` command.") {
		t.Fatalf("writeError did not report the missing subcommand:\n%s", got)
	}
}

// A command that already rendered its own bounded block must not produce a
// second one, so an --apply failure stays at one diagnostic per invocation.
func TestWriteErrorDoesNotRepeatPresentedDiagnostic(t *testing.T) {
	cause := &githubstack.CommandError{
		Command: "gh stack link --base synthetic-main synthetic-a synthetic-b",
		Cause:   exitError(t, 1),
		Output:  "synthetic rejected push",
	}

	var applied strings.Builder
	presented := writeNotApplied(&applied, Presentation{}, cause)
	if !alreadyPresented(presented) {
		t.Fatal("writeNotApplied did not mark the rendered error as presented")
	}

	var out strings.Builder
	writeError(&out, presented)

	if count := strings.Count(out.String(), "synthetic rejected push"); count != 0 {
		t.Fatalf("writeError repeated a presented diagnostic %d times:\n%s", count, out.String())
	}
}

// Wrapping must not lose the marker, because the submit and render paths add
// context around an error writeNotApplied already presented.
func TestWriteErrorTracksPresentedThroughWrapping(t *testing.T) {
	cause := &githubstack.CommandError{Command: "gh stack link --base a b c", Cause: exitError(t, 1), Output: "synthetic detail"}

	var applied strings.Builder
	wrapped := fmt.Errorf("submission spec retained at %s: %w", "/tmp/synthetic", writeNotApplied(&applied, Presentation{}, cause))

	var out strings.Builder
	writeError(&out, wrapped)

	if strings.Contains(out.String(), "synthetic detail") {
		t.Fatalf("wrapping lost the presented marker:\n%s", out.String())
	}
}

func TestWriteErrorLeavesPlainErrorsAlone(t *testing.T) {
	var out strings.Builder
	writeError(&out, errors.New("selected branch \"synthetic-a\" is not a local branch"))

	if got, want := out.String(), "error: selected branch \"synthetic-a\" is not a local branch\n"; got != want {
		t.Fatalf("writeError output = %q, want %q", got, want)
	}
}

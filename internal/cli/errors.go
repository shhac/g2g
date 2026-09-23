package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/subprocess"
)

// ghAuthExitCode is the exit status the GitHub CLI uses for an authentication
// failure. Recognizing it lets g2g add the remediation without spending an
// extra API call on a separate `gh auth status` probe.
const ghAuthExitCode = 4

// stoppedExitCode reports a command that did part of what it was asked and
// stopped somewhere a person has to act.
//
// Distinct from the failure code, because the two want opposite responses. A
// failure achieved nothing and can be retried; this achieved some of it, and
// what it achieved is not coming back — a merged pull request stays merged, a
// replayed branch stays replayed. Git says the same thing the same way: rebase
// and merge both exit non-zero when they stop needing you.
//
// It was zero, which is the one answer that is wrong for both readers: a script
// could not tell a finished descent from one that stopped after two merges, and
// the only signal that anything was unfinished was prose on stdout.
const stoppedExitCode = 3

// stoppedError marks a command that stopped part-way having already said so.
//
// The report is on stdout with the detail in it, so the top-level printer says
// nothing further: "error:" in front of a summary of what is already displayed
// reads as a second, different problem.
type stoppedError struct{ err error }

func (e stoppedError) Error() string { return e.err.Error() }
func (e stoppedError) Unwrap() error { return e.err }

// stoppedPartWay marks a report that has been written, so the exit status can
// carry what the prose already said.
func stoppedPartWay(err error) error { return stoppedError{err} }

func wasStopped(err error) bool {
	var stopped stoppedError
	return errors.As(err, &stopped)
}

// presentedError marks an error whose bounded diagnostic a command already
// rendered. The top-level printer then reports the failure without repeating
// that block, keeping one diagnostic per invocation.
type presentedError struct{ err error }

func (e presentedError) Error() string { return e.err.Error() }
func (e presentedError) Unwrap() error { return e.err }

func alreadyPresented(err error) bool {
	var presented presentedError
	return errors.As(err, &presented)
}

// writeError reports a failed command on stderr. Preview and status paths
// never render a diagnostic themselves, so without this the actionable part of
// an external CLI failure — an authentication prompt, an unknown subcommand —
// is discarded and only the exit status survives.
func writeError(writer io.Writer, err error) {
	// stderr carries no decoration at all, so a sentence that marked a command
	// for the renderer has its marks dropped rather than drawn. This is the
	// boundary that lets any sentence in this package name a command without
	// its author having to know whether it can also become an error.
	fmt.Fprintln(writer, "error:", plainCommands(err.Error()))
	if !alreadyPresented(err) {
		if diagnostic := commandDiagnostic(err); diagnostic != "" {
			fmt.Fprintln(writer)
			for _, line := range strings.Split(diagnostic, "\n") {
				fmt.Fprintln(writer, "  "+line)
			}
		}
	}
	if hint := remediationHint(err); hint != "" {
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, hint)
	}
}

func commandDiagnostic(err error) string {
	var commandErr *githubstack.CommandError
	if errors.As(err, &commandErr) {
		return commandErr.Diagnostic()
	}
	return ""
}

// remediationHint turns a recognized external-CLI failure into one actionable
// line. It only reads the error already returned by a call g2g had to make.
func remediationHint(err error) string {
	var commandErr *githubstack.CommandError
	if !errors.As(err, &commandErr) || !strings.HasPrefix(commandErr.Command, "gh ") {
		return ""
	}
	if code, exited := subprocess.ExitCode(commandErr.Cause); exited && code == ghAuthExitCode {
		return "GitHub CLI authentication is required. Run: gh auth login"
	}
	if strings.Contains(commandErr.Output, "unknown command \"stack\"") {
		return "This gh build has no `stack` command. Upgrade gh, then retry."
	}
	return ""
}

// foundError is a doctor that found something. The report is already on
// stdout, so like a stop part-way it adds nothing on stderr; it has its own
// exit status because a script asking "is anything wrong" wants the answer and
// not an error.
type foundError struct{ count int }

func (e foundError) Error() string {
	return fmt.Sprintf("found %s", count(e.count, "problem", "problems"))
}

func foundProblems(count int) error { return foundError{count} }

func foundSomething(err error) bool {
	var found foundError
	return errors.As(err, &found)
}

// foundExitCode is doctor's answer that something needs putting right: 0
// healthy, 1 found, 2 could not tell — the convention diff and grep use.
const foundExitCode = 1

// failedExitCode is a command that could not do what it was asked.
const failedExitCode = 2

// exitCode is the status a command's result exits with. A command that
// stopped part-way, or a doctor that found something, has already reported it
// in more detail than a one-line error could, so all that is left of either is
// the status.
func exitCode(err error) int {
	switch {
	case err == nil:
		return 0
	case wasStopped(err):
		return stoppedExitCode
	case foundSomething(err):
		return foundExitCode
	default:
		return failedExitCode
	}
}

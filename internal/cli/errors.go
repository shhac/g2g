package cli

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/shhac/gt2gh/internal/githubstack"
)

// ghAuthExitCode is the exit status the GitHub CLI uses for an authentication
// failure. Recognizing it lets gt2gh add the remediation without spending an
// extra API call on a separate `gh auth status` probe.
const ghAuthExitCode = 4

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
	fmt.Fprintln(writer, "error:", err)
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
// line. It only reads the error already returned by a call gt2gh had to make.
func remediationHint(err error) string {
	var commandErr *githubstack.CommandError
	if !errors.As(err, &commandErr) || !strings.HasPrefix(commandErr.Command, "gh ") {
		return ""
	}
	var exitErr *exec.ExitError
	if errors.As(commandErr.Cause, &exitErr) && exitErr.ExitCode() == ghAuthExitCode {
		return "GitHub CLI authentication is required. Run: gh auth login"
	}
	if strings.Contains(commandErr.Output, "unknown command \"stack\"") {
		return "This gh build has no `stack` command. Upgrade gh, then retry."
	}
	return ""
}

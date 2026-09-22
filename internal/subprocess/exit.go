package subprocess

import (
	"errors"
	"os/exec"
	"strconv"
)

// ExitError is a process that ran and exited with a status, for a Runner that
// spawns nothing.
//
// Callers read exit statuses through ExitCode rather than asserting
// *exec.ExitError themselves. Asserting the exec type outside this package
// meant only a real process could say "exit 1", so an injected fake could not
// reach the branch of a caller that treats one status as an answer rather
// than a failure.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return "exit status " + strconv.Itoa(e.Code) }

// ExitCode reports the status a process exited with, and whether err records
// one at all. A process killed by a signal still counts, with the -1 that
// os/exec reports for it; a process that never started does not.
func ExitCode(err error) (int, bool) {
	var execErr *exec.ExitError
	if errors.As(err, &execErr) {
		return execErr.ExitCode(), true
	}
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		return exitErr.Code, true
	}
	return 0, false
}

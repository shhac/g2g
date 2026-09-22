package subprocess

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"testing"
)

func TestExitCodeReadsARealProcessAndAFakeAlike(t *testing.T) {
	real := exec.Command("sh", "-c", "exit 3").Run()
	for name, test := range map[string]struct {
		err    error
		code   int
		exited bool
	}{
		"real process":  {err: real, code: 3, exited: true},
		"wrapped real":  {err: fmt.Errorf("git merge-base failed: %w", real), code: 3, exited: true},
		"fake":          {err: &ExitError{Code: 1}, code: 1, exited: true},
		"wrapped fake":  {err: fmt.Errorf("git update-ref failed: %w: synthetic reason", &ExitError{Code: 128}), code: 128, exited: true},
		"never started": {err: exec.ErrNotFound, exited: false},
		"cancelled":     {err: context.Canceled, exited: false},
		"no error":      {err: nil, exited: false},
		"unrelated":     {err: errors.New("synthetic failure"), exited: false},
	} {
		t.Run(name, func(t *testing.T) {
			code, exited := ExitCode(test.err)
			if exited != test.exited || code != test.code {
				t.Errorf("ExitCode(%v) = %d, %t; want %d, %t", test.err, code, exited, test.code, test.exited)
			}
		})
	}
}

// The diagnostic names the status a fake returned, which it could not while
// only *exec.ExitError carried one.
func TestObservingRunnerReportsAFakeExitStatus(t *testing.T) {
	if got := exitStatus(context.Background(), &ExitError{Code: 4}); got != "4" {
		t.Errorf("exitStatus = %q, want 4", got)
	}
}

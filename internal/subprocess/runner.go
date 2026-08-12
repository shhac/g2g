// Package subprocess provides the small boundary for invoking external CLIs.
package subprocess

import (
	"context"
	"os/exec"
)

// Runner executes programs by name. Future Graphite and GitHub adapters will
// receive this interface, allowing tests to replace real CLIs with PATH fakes.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// ExecRunner invokes programs from PATH using the host operating system.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

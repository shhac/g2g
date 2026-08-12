// Package subprocess provides the small boundary for invoking external CLIs.
package subprocess

import (
	"context"
	"errors"
	"os/exec"
	"strconv"
	"time"

	"github.com/shhac/gt2gh/internal/diagnostic"
)

// Runner executes programs by name. Future Graphite and GitHub adapters will
// receive this interface, allowing tests to replace real CLIs with PATH fakes.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// ExecRunner invokes programs from PATH using the host operating system.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if ctx.Err() != nil {
		return output, ctx.Err()
	}
	return output, err
}

// ObservingRunner adds opt-in context diagnostics without changing the child
// process command, environment, timeout, output, or error behavior.
type ObservingRunner struct{ Runner Runner }

func (r ObservingRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if r.Runner == nil {
		return nil, errors.New("subprocess runner is not configured")
	}
	command := diagnostic.SafeCommand(name, args)
	diagnostic.Event(ctx, "subprocess.start", diagnostic.Field{Key: "command", Value: command})
	started := time.Now()
	output, err := r.Runner.Run(ctx, name, args...)
	fields := []diagnostic.Field{
		{Key: "command", Value: command},
		{Key: "elapsed_ms", Value: strconv.FormatInt(time.Since(started).Milliseconds(), 10)},
		{Key: "status", Value: processStatus(ctx, err)},
		{Key: "exit", Value: exitStatus(ctx, err)},
	}
	diagnostic.Event(ctx, "subprocess.end", fields...)
	if err != nil {
		bounded := diagnostic.BoundedOutput(output)
		if bounded == "" {
			return output, err
		}
		diagnostic.Event(ctx, "subprocess.output", diagnostic.Field{Key: "command", Value: command}, diagnostic.Field{Key: "output", Value: bounded})
	}
	return output, err
}

func processStatus(ctx context.Context, err error) string {
	if ctx.Err() != nil {
		return "canceled"
	}
	if err != nil {
		return "error"
	}
	return "ok"
}

func exitStatus(ctx context.Context, err error) string {
	if ctx.Err() != nil {
		return "canceled"
	}
	if err == nil {
		return "0"
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return strconv.Itoa(exitErr.ExitCode())
	}
	return "error"
}

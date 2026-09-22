package subprocess

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/shhac/g2g/internal/diagnostic"
)

// RunInteractive runs a program attached to this process's terminal, for the
// one thing that needs it: an editor a person types into.
//
// It is here rather than beside its caller because this package is the only
// place a process is started. An editor launched from elsewhere was the one
// exception, and it ran with no diagnostic record of what was started.
func RunInteractive(ctx context.Context, name string, args ...string) error {
	command := exec.CommandContext(ctx, name, args...)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	safe := diagnostic.SafeCommand(name, args)
	diagnostic.Event(ctx, "subprocess.start", diagnostic.Field{Key: "command", Value: safe}, diagnostic.Field{Key: "interactive", Value: "true"})
	started := time.Now()
	err := command.Run()
	diagnostic.Event(ctx, "subprocess.end",
		diagnostic.Field{Key: "command", Value: safe},
		diagnostic.Field{Key: "elapsed_ms", Value: strconv.FormatInt(time.Since(started).Milliseconds(), 10)},
		diagnostic.Field{Key: "status", Value: processStatus(ctx, err)},
		diagnostic.Field{Key: "exit", Value: exitStatus(ctx, err)},
	)
	return err
}

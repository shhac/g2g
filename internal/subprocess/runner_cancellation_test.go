package subprocess

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shhac/gt2gh/internal/testutil"
)

func TestExecRunnerReturnsContextCancellation(t *testing.T) {
	testutil.WithFakeExecutables(t, map[string]string{"gt": "trap 'exit 0' TERM\nwhile :; do sleep 1; done\n"})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := (ExecRunner{}).Run(ctx, "gt")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %v, want deadline exceeded", err)
	}
}

// A cancelled wrapper script leaves its own children holding the inherited
// output pipe. Without a wait delay the deadline is reported on time but the
// call blocks until that grandchild exits, so a wedged gt or gh still hangs.
func TestExecRunnerDoesNotBlockOnSurvivingGrandchild(t *testing.T) {
	testutil.WithFakeExecutables(t, map[string]string{"gt": "sleep 30\n"})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	started := time.Now()
	_, err := (ExecRunner{}).Run(ctx, "gt")
	elapsed := time.Since(started)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %v, want deadline exceeded", err)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("Run() blocked for %s after cancellation; wait delay did not release the pipe", elapsed)
	}
}

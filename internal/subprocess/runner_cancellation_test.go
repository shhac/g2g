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

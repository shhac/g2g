package git

import (
	"context"
	"testing"

	"github.com/shhac/g2g/internal/subprocess"
)

// exitingRunner exits every call with one status and spawns nothing.
type exitingRunner struct{ code int }

func (r exitingRunner) Run(context.Context, string, ...string) ([]byte, error) {
	return nil, &subprocess.ExitError{Code: r.code}
}

// Each of these reads an exit status as an answer rather than a failure. They
// asserted *exec.ExitError themselves, so only a real process could reach that
// answer and an injected fake always fell through to the error.
func TestExitStatusesAreAnswersForAnInjectedRunner(t *testing.T) {
	ctx := context.Background()

	ancestor, err := (Client{Runner: exitingRunner{code: 1}}).IsAncestor(ctx, "synthetic-lower", "synthetic-top")
	if err != nil || ancestor {
		t.Errorf("IsAncestor on exit 1 = %t, %v; want false, nil", ancestor, err)
	}
	if _, err := (Client{Runner: exitingRunner{code: 128}}).IsAncestor(ctx, "synthetic-lower", "synthetic-top"); err == nil {
		t.Error("IsAncestor on exit 128 = nil error; only exit 1 means no")
	}

	if err := (Client{Runner: exitingRunner{code: 1}}).UnpinForkPoint(ctx, "synthetic-gone"); err != nil {
		t.Errorf("UnpinForkPoint of a missing pin = %v, want nil", err)
	}

	updates, clean, err := (Client{Runner: exitingRunner{code: 1}}).PreviewReplay(ctx, "synthetic-trunk", []Range{{From: "synthetic-fork", To: "synthetic-top"}})
	if err != nil || clean || updates != nil {
		t.Errorf("PreviewReplay on a conflicting replay = %v, %t, %v; want a conflict, not an error", updates, clean, err)
	}
}

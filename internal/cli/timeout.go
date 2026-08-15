package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

const (
	// discoveryTimeout bounds read-only discovery and revalidation: a handful
	// of local CLI invocations plus two GitHub reads. Set generously, because
	// this ceiling is newly enforced — the deadline never actually fired
	// before — and a large repository or a slow network should not start
	// failing where it previously succeeded. --timeout narrows it.
	discoveryTimeout = 45 * time.Second
	// mutationBase and mutationPerBranch bound the mutation phase, which gets
	// its own budget so discovery cost can never shorten it. submit performs
	// one push plus a pull-request creation per branch, so the ceiling scales
	// with the selected stack.
	mutationBase      = 60 * time.Second
	mutationPerBranch = 30 * time.Second

	completionTimeout = 3 * time.Second
)

// budgets derives per-phase deadlines from the root --timeout flag. Phases are
// bounded separately and both descend from the command context rather than
// from each other, so a slow discovery pass cannot leave a mutation with
// almost no time and be cancelled halfway through.
type budgets struct{ override time.Duration }

func newBudgets(cmd *cobra.Command) budgets {
	override, _ := cmd.Flags().GetDuration("timeout")
	return budgets{override: override}
}

func (b budgets) discovery(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, b.limit(discoveryTimeout))
}

func (b budgets) mutation(ctx context.Context, branches int) (context.Context, context.CancelFunc) {
	budget := mutationBase + time.Duration(branches)*mutationPerBranch
	return context.WithTimeout(ctx, b.limit(budget))
}

func (b budgets) limit(fallback time.Duration) time.Duration {
	if b.override > 0 {
		return b.override
	}
	return fallback
}

// mutationTimeout reports an expired mutation budget as its own failure. A
// generic "context deadline exceeded" gives no indication that an external
// command may have partly succeeded, which is the fact a caller needs most.
func mutationTimeout(err error, recovery string) error {
	if !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("timed out during the mutation phase; the external command may have partly completed. %s (raise the ceiling with --timeout)", recovery)
}

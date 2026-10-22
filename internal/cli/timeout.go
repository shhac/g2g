package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	// discoveryTimeout bounds read-only discovery and revalidation: a handful
	// of local CLI invocations plus two GitHub reads. Set generously, because
	// this ceiling is newly enforced — the deadline never actually fired
	// before — and a large repository or a slow network should not start
	// failing where it previously succeeded. Each planning pass gets its own
	// budget; --timeout replaces it and G2G_TIMEOUT can raise its default.
	discoveryTimeout = 45 * time.Second
	// mutationBase and mutationPerBranch bound the mutation phase, which gets
	// its own budget so discovery cost can never shorten it. submit performs
	// one push plus a pull-request creation per branch, so the ceiling scales
	// with the selected stack.
	mutationBase      = 60 * time.Second
	mutationPerBranch = 30 * time.Second
	// landingPerBranch is what one branch of a descent can take: a push, two
	// waits on GitHub, a merge, a replay and the tidying after it. The waits
	// are the reason it is not mutationPerBranch -- they are bounded by this
	// and by nothing else, so a ceiling that fitted the calls alone would cut
	// a merge off mid-flight.
	landingPerBranch = 180 * time.Second
	// interruptedReadBudget bounds what a command reads to report a mutation
	// that stopped part-way: whether a replay was left journaled, and on which
	// files. They are local reads, asked after the mutation's own budget may
	// have run out.
	interruptedReadBudget = 10 * time.Second
)

// completionTimeout bounds a completion, which runs on a keystroke and must
// answer or give up quickly. It is a variable only so the test build can lift
// it: a suite that spawns fake executables on a loaded machine can take longer
// than a keystroke's patience without anything being wrong, and a ceiling that
// fails tests by machine speed tests the machine.
var completionTimeout = defaultCompletionTimeout

const defaultCompletionTimeout = 3 * time.Second

// budgets derives per-phase deadlines from --timeout and G2G_TIMEOUT. Phases
// are bounded separately and descend from the command context rather than
// from each other, so a slow discovery pass cannot leave a mutation with
// almost no time and be cancelled halfway through.
type budgets struct {
	override time.Duration
	minimum  time.Duration
}

func newBudgets(cmd *cobra.Command) budgets {
	override, _ := cmd.Flags().GetDuration("timeout")
	minimum := time.Duration(0)
	// The root pre-run validates this value; explicit flags ignore it entirely.
	if !cmd.Flags().Changed("timeout") {
		minimum, _ = time.ParseDuration(os.Getenv("G2G_TIMEOUT"))
	}
	return budgets{override: override, minimum: minimum}
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
	return max(fallback, b.minimum)
}

// mutationTimeout reports an expired mutation budget as its own failure. A
// generic "context deadline exceeded" gives no indication that an external
// command may have partly succeeded, which is the fact a caller needs most.
// landing is the mutation ceiling for a command that waits on a remote between
// its calls.
func (b budgets) landing(ctx context.Context, branches int) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, b.limit(mutationBase+time.Duration(branches)*landingPerBranch))
}

// discoveryTimedOut names the phase and the way out.
//
// What comes back from a discovery that runs out of time is whichever Git call
// happened to be in flight, which on a large checkout is a pair of branches the
// command was never asked about. That is true and useless: it reads as a fault
// in two branches rather than as a ceiling, and nothing in it suggests raising
// one. Nobody should have to know that a rev-list between two strangers means
// "this repository is bigger than the default allows".
//
// It names the ceiling that was actually in force. Saying "the 45s default" to
// someone who had already passed --timeout 10m sent them to raise a flag they
// had raised.
func (b budgets) discoveryTimedOut(err error) error {
	if !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if b.override > 0 {
		return fmt.Errorf("timed out working out what to do, before anything was changed · it ran out of the %s --timeout allowed", b.override)
	}
	if b.minimum > discoveryTimeout {
		return fmt.Errorf("timed out working out what to do, before anything was changed · it ran out of the %s G2G_TIMEOUT minimum budget (raise it with --timeout or G2G_TIMEOUT)", b.minimum)
	}
	return fmt.Errorf("timed out working out what to do, before anything was changed · a large repository can need longer than the %s default (raise it with --timeout)", discoveryTimeout)
}

func mutationTimeout(err error, recovery string) error {
	if !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("timed out during the mutation phase; the external command may have partly completed. %s (raise the ceiling with --timeout)", recovery)
}

// validateTimeout resolves persistent timeout policy before a command reads or
// changes anything. Help and shell completion remain usable with a bad default.
func validateTimeout(cmd *cobra.Command, _ []string) error {
	if cmd.Name() == "completion" || strings.HasPrefix(cmd.Name(), "__complete") || cmd.Name() == "help" || cmd == cmd.Root() {
		return nil
	}
	if cmd.Flags().Changed("timeout") {
		value, err := cmd.Flags().GetDuration("timeout")
		if err != nil || value <= 0 {
			return fmt.Errorf("--timeout must be a positive duration")
		}
		return nil
	}
	if value, present := os.LookupEnv("G2G_TIMEOUT"); present {
		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 {
			return fmt.Errorf("G2G_TIMEOUT must be a positive duration (for example 3m)")
		}
	}
	return nil
}

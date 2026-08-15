package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func budgetCommand(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "root", RunE: func(*cobra.Command, []string) error { return nil }}
	cmd.PersistentFlags().Duration("timeout", 0, "")
	cmd.SetArgs(args)
	cmd.SetOut(new(strings.Builder))
	cmd.SetErr(new(strings.Builder))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	return cmd
}

func remaining(t *testing.T, ctx context.Context) time.Duration {
	t.Helper()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("context has no deadline")
	}
	return time.Until(deadline)
}

// The deadline used to be discarded: every command built a timeout context and
// then reassigned the variable from commandContext, which derived from
// cmd.Context(). This asserts the decorated context still carries it.
func TestCommandContextPreservesCallerDeadline(t *testing.T) {
	cmd := budgetCommand(t)
	cmd.SetContext(context.Background())

	ctx, cancel := newBudgets(cmd).discovery(cmd.Context())
	defer cancel()

	decorated := commandContext(ctx, cmd, "link", "preview", "", "")
	if _, ok := decorated.Deadline(); !ok {
		t.Fatal("commandContext dropped the caller's deadline")
	}
	if got := remaining(t, decorated); got > discoveryTimeout || got < discoveryTimeout-time.Second {
		t.Fatalf("remaining budget = %s, want close to %s", got, discoveryTimeout)
	}
}

// The mutation phase must descend from the command context, not from the
// discovery context, so a slow discovery pass cannot leave it a sliver of time
// and cancel a push or PR creation partway through.
func TestMutationBudgetIsNotShortenedByDiscovery(t *testing.T) {
	cmd := budgetCommand(t)
	cmd.SetContext(context.Background())
	budgets := newBudgets(cmd)

	root := commandContext(cmd.Context(), cmd, "submit", "apply", "", "")
	discovery, cancelDiscovery := budgets.discovery(root)
	defer cancelDiscovery()
	if _, ok := discovery.Deadline(); !ok {
		t.Fatal("discovery context has no deadline")
	}

	mutation, cancelMutation := budgets.mutation(root, 4)
	defer cancelMutation()

	want := mutationBase + 4*mutationPerBranch
	if got := remaining(t, mutation); got <= discoveryTimeout || got > want {
		t.Fatalf("mutation budget = %s, want between %s and %s", got, discoveryTimeout, want)
	}
}

func TestMutationBudgetScalesWithBranchCount(t *testing.T) {
	cmd := budgetCommand(t)
	cmd.SetContext(context.Background())
	budgets := newBudgets(cmd)

	small, cancelSmall := budgets.mutation(context.Background(), 1)
	defer cancelSmall()
	large, cancelLarge := budgets.mutation(context.Background(), 6)
	defer cancelLarge()

	if remaining(t, large)-remaining(t, small) < 4*mutationPerBranch {
		t.Fatal("mutation budget did not scale with the selected stack size")
	}
}

func TestTimeoutFlagOverridesBothPhases(t *testing.T) {
	cmd := budgetCommand(t, "--timeout", "5s")
	cmd.SetContext(context.Background())
	budgets := newBudgets(cmd)

	for name, ctx := range map[string]context.Context{
		"discovery": firstOf(budgets.discovery(context.Background())),
		"mutation":  firstOf(budgets.mutation(context.Background(), 8)),
	} {
		if got := remaining(t, ctx); got > 5*time.Second || got < 4*time.Second {
			t.Fatalf("%s budget = %s, want ~5s", name, got)
		}
	}
}

func firstOf(ctx context.Context, cancel context.CancelFunc) context.Context {
	_ = cancel
	return ctx
}

func TestMutationTimeoutExplainsPartialCompletion(t *testing.T) {
	err := mutationTimeout(context.DeadlineExceeded, "Re-running is safe.")

	for _, want := range []string{"mutation phase", "may have partly completed", "Re-running is safe.", "--timeout"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err, want)
		}
	}
}

func TestMutationTimeoutLeavesOtherErrorsUnchanged(t *testing.T) {
	cause := errors.New("gh stack link failed: exit status 1")
	if got := mutationTimeout(cause, "Re-running is safe."); got != cause {
		t.Fatalf("mutationTimeout rewrote a non-deadline error: %v", got)
	}
}

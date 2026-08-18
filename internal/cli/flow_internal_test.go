package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// interruptedFlow drives applyFlow's failure branch directly, which is the only
// way to reach the interrupted hook without standing up a whole command.
type interruptedPlan struct{ name string }

func interruptedFlow(t *testing.T, claim bool) (string, error) {
	t.Helper()

	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	reported := false
	flow := applyFlow[interruptedPlan]{
		plan: func(context.Context) (interruptedPlan, error) { return interruptedPlan{name: "synthetic"}, nil },
		revalidate: func(_ context.Context, plan interruptedPlan) (interruptedPlan, error) {
			return plan, nil
		},
		render: func(w io.Writer, _ interruptedPlan, _ Presentation) error {
			_, err := fmt.Fprintln(w, "synthetic plan")
			return err
		},
		execute: func(context.Context, interruptedPlan) error { return fmt.Errorf("synthetic mutation failure") },
		interrupted: func(context.Context, error) (bool, error) {
			if !claim {
				return false, nil
			}
			reported = true
			// A report helper returns nil on a successful write, which is
			// exactly the value that used to mean "not my case".
			return true, prose(cmd.OutOrStdout(), Presentation{}, "The replay stopped part-way.")
		},
		notices: flowNotices{applied: "Applied.", changed: "Changed."},
	}
	err := flow.run(cmd, context.Background(), newBudgets(cmd), Presentation{}, true)
	if claim && !reported {
		t.Fatal("the hook never ran")
	}
	return out.String(), err
}

// A hook that claims the failure owns the report, and the flow must not add its
// own underneath. Returning the report directly said "not my case" whenever
// writing it succeeded, so a stopped sync printed both messages and exited
// non-zero.
func TestAClaimedInterruptionIsReportedOnlyOnce(t *testing.T) {
	out, err := interruptedFlow(t, true)

	if err != nil {
		t.Errorf("a claimed interruption returned an error: %v", err)
	}
	if !strings.Contains(out, "stopped part-way") {
		t.Errorf("the hook's report is missing:\n%s", out)
	}
	if strings.Contains(out, "Not applied") {
		t.Errorf("the flow reported again underneath the hook:\n%s", out)
	}
}

// A hook that declines leaves the ordinary failure path exactly as it was.
func TestAnUnclaimedFailureStillSaysNotApplied(t *testing.T) {
	out, err := interruptedFlow(t, false)

	if err == nil {
		t.Error("an unclaimed mutation failure returned no error")
	}
	if !strings.Contains(out, "Not applied") {
		t.Errorf("the ordinary failure path did not run:\n%s", out)
	}
}

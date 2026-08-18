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

// A preview closes by telling the reader what to do next. When the plan is
// already blocked, "rerun with --apply" is advice for a command that will
// refuse — and the rendered view says "Apply blocked" three lines above it, so
// the two contradicted each other in one screen.
func TestABlockedPreviewDoesNotInviteAnApply(t *testing.T) {
	for _, test := range []struct {
		name    string
		blocked string
		want    string
		absent  string
	}{
		{name: "blocked", blocked: "a synthetic refusal", want: "Apply would refuse", absent: "Rerun with --apply"},
		{name: "clear", blocked: "", want: "Rerun with --apply", absent: "Apply would refuse"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cmd := &cobra.Command{}
			var out bytes.Buffer
			cmd.SetOut(&out)
			flow := applyFlow[interruptedPlan]{
				plan: func(context.Context) (interruptedPlan, error) { return interruptedPlan{}, nil },
				render: func(w io.Writer, _ interruptedPlan, _ Presentation) error {
					_, err := fmt.Fprintln(w, "synthetic plan")
					return err
				},
				blocked: func(interruptedPlan) string { return test.blocked },
				notices: flowNotices{preview: "Rerun with --apply to replay these commits."},
			}
			if err := flow.run(cmd, context.Background(), newBudgets(cmd), Presentation{}, false); err != nil {
				t.Fatalf("preview error = %v", err)
			}

			if !strings.Contains(out.String(), test.want) {
				t.Errorf("preview does not say %q:\n%s", test.want, out.String())
			}
			if strings.Contains(out.String(), test.absent) {
				t.Errorf("preview still says %q:\n%s", test.absent, out.String())
			}
		})
	}
}

// A suggested next step is an optional continuation after a completed mutation,
// never recovery guidance. Keeping it in the shared flow makes that boundary
// hold for every command that opts in.
func TestSuggestedNextStepOnlyFollowsASuccessfulHumanApply(t *testing.T) {
	for _, test := range []struct {
		name    string
		p       Presentation
		blocked string
		want    string
		absent  string
	}{
		{name: "human success", want: "Suggested next step: g2g status"},
		{name: "blocked", blocked: "a synthetic refusal", absent: "Suggested next step:"},
		{name: "json", p: Presentation{Format: formatJSON}, absent: "Suggested next step:"},
		{name: "porcelain", p: Presentation{Format: formatPorcelain}, absent: "Suggested next step:"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cmd := &cobra.Command{}
			var out bytes.Buffer
			cmd.SetOut(&out)
			flow := applyFlow[interruptedPlan]{
				plan: func(context.Context) (interruptedPlan, error) { return interruptedPlan{}, nil },
				revalidate: func(_ context.Context, plan interruptedPlan) (interruptedPlan, error) {
					return plan, nil
				},
				render: func(w io.Writer, _ interruptedPlan, _ Presentation) error {
					_, err := fmt.Fprintln(w, "synthetic plan")
					return err
				},
				execute: func(context.Context, interruptedPlan) error { return nil },
				blocked: func(interruptedPlan) string { return test.blocked },
				notices: flowNotices{
					applied:       "Applied.",
					changed:       "Changed.",
					suggestedNext: "g2g status",
				},
			}
			err := flow.run(cmd, context.Background(), newBudgets(cmd), test.p, true)
			if test.blocked != "" {
				if err == nil {
					t.Fatal("blocked apply returned nil")
				}
			} else if err != nil {
				t.Fatalf("successful apply error = %v", err)
			}
			if test.want != "" && !strings.Contains(out.String(), test.want) {
				t.Errorf("output does not contain %q:\n%s", test.want, out.String())
			}
			if test.absent != "" && strings.Contains(out.String(), test.absent) {
				t.Errorf("output unexpectedly contains %q:\n%s", test.absent, out.String())
			}
			if test.p.machine() {
				if got, want := out.String(), "synthetic plan\n"; got != want {
					t.Errorf("machine output = %q, want unchanged %q", got, want)
				}
			}
		})
	}
}

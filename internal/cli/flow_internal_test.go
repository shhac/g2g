package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/link"
	"github.com/shhac/g2g/internal/prune"
	"github.com/shhac/g2g/internal/stack"
)

// interruptedFlow drives applyFlow's failure branch directly, which is the only
// way to reach the interrupted hook without standing up a whole command.
type interruptedPlan struct{ name string }

func (p interruptedPlan) Equal(other interruptedPlan) bool { return p == other }

func interruptedFlow(t *testing.T, claim bool) (string, error) {
	t.Helper()

	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	reported := false
	flow := applyFlow[interruptedPlan]{
		plan: func(context.Context) (interruptedPlan, error) { return interruptedPlan{name: "synthetic"}, nil },
		render: func(w io.Writer, _ interruptedPlan, _ Presentation) error {
			_, err := fmt.Fprintln(w, "synthetic plan")
			return err
		},
		execute: func(context.Context, interruptedPlan) error { return fmt.Errorf("synthetic mutation failure") },
		interrupted: func(context.Context, interruptedPlan, error) (bool, error) {
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
		{name: "human success", want: "Suggested next step: g2g github status"},
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
				render: func(w io.Writer, _ interruptedPlan, _ Presentation) error {
					_, err := fmt.Fprintln(w, "synthetic plan")
					return err
				},
				execute: func(context.Context, interruptedPlan) error { return nil },
				blocked: func(interruptedPlan) string { return test.blocked },
				suggest: always[interruptedPlan]("g2g github status"),
				notices: flowNotices{
					applied: "Applied.",
					changed: "Changed.",
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

// sequencedFlow records the order the flow asks its hooks in. Each plan call
// answers the next name, so a test chooses whether the world moved.
func sequencedFlow(calls *[]string, names ...string) applyFlow[interruptedPlan] {
	planned := 0
	return applyFlow[interruptedPlan]{
		plan: func(context.Context) (interruptedPlan, error) {
			*calls = append(*calls, "plan")
			name := names[min(planned, len(names)-1)]
			planned++
			return interruptedPlan{name: name}, nil
		},
		revalidation: revalidation{"synthetic", "synthetic plan"},
		render: func(w io.Writer, _ interruptedPlan, _ Presentation) error {
			_, err := fmt.Fprintln(w, "synthetic plan")
			return err
		},
		execute: func(_ context.Context, plan interruptedPlan) error {
			*calls = append(*calls, "execute "+plan.name)
			return nil
		},
		notices: flowNotices{applied: "Applied.", changed: "Changed."},
	}
}

func runSequenced(t *testing.T, flow applyFlow[interruptedPlan], apply bool) (string, error) {
	t.Helper()
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	err := flow.run(cmd, context.Background(), newBudgets(cmd), Presentation{}, apply)
	return out.String(), err
}

// The flow asks plan again immediately before mutating and refuses an answer
// that differs from the preview. Each service used to do this itself, and a
// command was free to hand the flow a revalidation that compared nothing.
func TestAnApplyReplansAndRefusesWhatMovedUnderneath(t *testing.T) {
	var calls []string
	out, err := runSequenced(t, sequencedFlow(&calls, "before", "after"), true)

	if err == nil || !strings.Contains(err.Error(), "synthetic plan changed during revalidation") {
		t.Fatalf("error = %v, want the revalidation refusal", err)
	}
	if !strings.Contains(out, "Not applied") {
		t.Errorf("the refusal was not reported:\n%s", out)
	}
	if got := strings.Join(calls, ","); got != "plan,plan" {
		t.Errorf("calls = %s, want two plans and no execute", got)
	}
}

func TestAnApplyOfAnUnchangedPlanExecutesTheSecondAnswer(t *testing.T) {
	var calls []string
	if _, err := runSequenced(t, sequencedFlow(&calls, "same"), true); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(calls, ","); got != "plan,plan,execute same" {
		t.Errorf("calls = %s", got)
	}
}

// precheck refuses before anything is re-discovered: a dirty tree must not
// cost a round trip to GitHub, or reach one at all. A preview never asks it.
func TestPrecheckRunsBeforeReplanningAndOnlyOnApply(t *testing.T) {
	var calls []string
	flow := sequencedFlow(&calls, "same")
	flow.precheck = func(context.Context) error {
		calls = append(calls, "precheck")
		return errors.New("synthetic dirty tree")
	}
	if _, err := runSequenced(t, flow, true); err == nil || !strings.Contains(err.Error(), "synthetic dirty tree") {
		t.Fatalf("error = %v, want the precheck's", err)
	}
	if got := strings.Join(calls, ","); got != "plan,precheck" {
		t.Errorf("apply calls = %s, want the precheck before any second plan", got)
	}

	calls = nil
	if _, err := runSequenced(t, flow, false); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(calls, ","); got != "plan" {
		t.Errorf("preview calls = %s, want no precheck", got)
	}
}

// settle follows the comparison, so a plan that moved is reported as moved
// rather than as whatever settle would refuse, and what it adds reaches the
// mutation. A preview never asks it.
func TestSettleFollowsTheComparisonAndOnlyOnApply(t *testing.T) {
	var calls []string
	flow := sequencedFlow(&calls, "same")
	flow.settle = func(_ context.Context, plan interruptedPlan) (interruptedPlan, error) {
		calls = append(calls, "settle")
		plan.name += " settled"
		return plan, nil
	}
	if _, err := runSequenced(t, flow, true); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(calls, ","); got != "plan,plan,settle,execute same settled" {
		t.Errorf("apply calls = %s", got)
	}

	calls = nil
	if _, err := runSequenced(t, flow, false); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(calls, ","); got != "plan" {
		t.Errorf("preview calls = %s, want no settle", got)
	}

	calls = nil
	moved := sequencedFlow(&calls, "before", "after")
	moved.settle = flow.settle
	if _, err := runSequenced(t, moved, true); err == nil || !strings.Contains(err.Error(), "changed during revalidation") {
		t.Fatalf("error = %v, want the comparison to refuse first", err)
	}
	if slices.Contains(calls, "settle") {
		t.Error("settle ran for a plan that moved")
	}
}

// A plan the command already discovered stands in for the first ask, and an
// apply still asks again before mutating.
func TestADiscoveredPlanIsStillReplannedBeforeMutating(t *testing.T) {
	var calls []string
	flow := sequencedFlow(&calls, "after")
	flow.discovered = &interruptedPlan{name: "before"}
	if _, err := runSequenced(t, flow, true); err == nil || !strings.Contains(err.Error(), "changed during revalidation") {
		t.Fatalf("error = %v, want the discovered plan compared with a fresh one", err)
	}
	if got := strings.Join(calls, ","); got != "plan" {
		t.Errorf("calls = %s, want exactly the one re-plan", got)
	}
}

// A wrapper plan's Equal is what an apply compares, so it must see a change
// in the plan it wraps and ignore what only shapes a suggestion or a label.
func TestWrapperPlansCompareTheWrappedPlanAlone(t *testing.T) {
	landed := prune.Plan{Landed: []string{"synthetic-a"}}
	if !(prunePlan{Plan: landed, remote: "origin"}).Equal(prunePlan{Plan: landed, remote: "synthetic-remote", unpublished: true}) {
		t.Error("prunePlan compared what only its suggestion reads")
	}
	if (prunePlan{Plan: landed}).Equal(prunePlan{Plan: prune.Plan{Landed: []string{"synthetic-b"}}}) {
		t.Error("prunePlan missed a change in what it forgets")
	}
	parent := graph.TrackPlan{Parent: "synthetic-main"}
	if !(trackPlan{parent, false}).Equal(trackPlan{parent, true}) {
		t.Error("trackPlan compared whether another record describes the repository")
	}
	if (trackPlan{TrackPlan: parent}).Equal(trackPlan{TrackPlan: graph.TrackPlan{Parent: "synthetic-other"}}) {
		t.Error("trackPlan missed a change of parent")
	}
	linked := link.Plan{Discovery: stack.Discovery{Snapshot: stack.Snapshot{Branches: []string{"synthetic-a"}}}}
	if (unlinkPlan{Plan: linked}).Equal(unlinkPlan{Plan: link.Plan{Discovery: stack.Discovery{Snapshot: stack.Snapshot{Branches: []string{"synthetic-b"}}}}}) {
		t.Error("unlinkPlan missed a change in the discovery its number comes from")
	}
}

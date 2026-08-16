package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/shhac/gt2gh/internal/githubstack"
	"github.com/shhac/gt2gh/internal/link"
	"github.com/shhac/gt2gh/internal/stack"
	"github.com/spf13/cobra"
)

func newStatus(service link.Service, completions stack.Completions, presentation Presentation) *cobra.Command {
	var selection stackOptions
	cmd := &cobra.Command{Use: "status", GroupID: groupPublish, Short: "Inspect a stack, its pull requests, and native GitHub membership (read-only)", Args: cobra.NoArgs}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		presentation := presentation.resolve(cmd)
		ctx, cancel := newBudgets(cmd).discovery(commandContext(cmd.Context(), cmd, "status", "read_only", selection.branch, selection.trunk))
		defer cancel()
		plan, err := service.Plan(ctx, selection.Selection())
		if err != nil {
			return err
		}
		return writeStatus(cmd.OutOrStdout(), plan, presentation)
	}
	selection.register(cmd, completions, "local branch to inspect (defaults to current branch)", "trunk to use as the base")
	return cmd
}

// membershipView is the graph both status and unlink render: the selected path
// with each branch's pull request and native-stack membership. They differ only
// in the notes below it, so unlink composes this rather than building status's
// whole projection and discarding half of it.
func membershipView(plan link.Plan, operation string) (stackView, githubstack.Membership) {
	issues := map[string]string{}
	for _, issue := range plan.Issues {
		issues[issue.Branch] = issue.Reason
	}
	native := githubstack.AssessMembership(plan.Branches, plan.PullRequests)

	view := stackView{
		Operation:    operation,
		Target:       plan.Target,
		TargetSource: plan.TargetSource,
		Nodes:        []stackNode{{Branch: plan.Base, Trunk: true}},
	}
	for _, branch := range plan.Branches {
		node := stackNode{Branch: branch, Target: branch == plan.Target}
		if reason := issues[branch]; reason != "" {
			node.State, node.Severity = "blocked: "+reason, severityBad
			view.Nodes = append(view.Nodes, node)
			continue
		}
		pr := native.Branches[branch]
		node.PRNumber, node.PRURL = pr.Number, pr.URL
		node.State, node.Severity = "aligned", severityOK
		if marker, markerSeverity := membershipStyle(native, branch); marker != "" {
			node.State, node.Severity = marker, markerSeverity
		}
		view.Nodes = append(view.Nodes, node)
	}
	return view, native
}

// statusAdvice reuses the blocked-reason logic so triage and the command that
// fixes it never disagree, phrased for a read-only report.
func statusAdvice(plan link.Plan) string {
	return strings.Replace(blockedReason(plan), "Apply blocked: ", "Safe next action: ", 1)
}

func statusView(plan link.Plan) stackView {
	view, native := membershipView(plan, "status")
	if len(plan.Issues) != 0 {
		return view.block(statusAdvice(plan))
	}
	return view.note(nativeMessage(native), membershipNoteSeverity(native.State))
}

func writeStatus(writer io.Writer, plan link.Plan, p Presentation) error {
	return writeStackView(writer, statusView(plan), p)
}

// membershipStyle answers everything a renderer needs about one branch's
// native-stack membership in a single place. Three functions switching on the
// same state meant its presentation was spread across three switches that had
// to agree.
func membershipStyle(membership githubstack.Membership, branch string) (marker string, nodeSeverity severity) {
	pr := membership.Branches[branch]
	switch membership.State {
	case githubstack.Partial:
		if pr.StackNumber == 0 {
			return "not linked", severityWarn
		}
	case githubstack.Conflicting:
		if pr.StackNumber == 0 {
			return "not linked", severityBad
		}
		return fmt.Sprintf("stack #%d, position %d", pr.StackNumber, pr.StackPosition), severityBad
	}
	return "", severityNeutral
}

func membershipNoteSeverity(state githubstack.MembershipState) severity {
	if state == githubstack.Conflicting {
		return severityBad
	}
	return severityNeutral
}

func nativeMessage(s githubstack.Membership) string {
	switch s.State {
	case githubstack.Aligned:
		return fmt.Sprintf("GitHub stack #%d · selected path %d/%d · aligned", s.StackNumber, s.Selected, s.StackSize)
	case githubstack.Partial:
		return fmt.Sprintf("GitHub stack #%d · partial (%d/%d linked) · run g2g link to add the marked PRs.", s.StackNumber, s.Linked, s.Selected)
	case githubstack.Conflicting:
		return "GitHub stack: conflicting membership · review the marked PRs before changing anything."
	default:
		return "GitHub stack: not linked · run g2g link to preview a link."
	}
}

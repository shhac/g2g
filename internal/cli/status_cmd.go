package cli

import (
	"fmt"
	"io"

	"github.com/shhac/gt2gh/internal/githubstack"
	"github.com/shhac/gt2gh/internal/link"
	"github.com/shhac/gt2gh/internal/stack"
	"github.com/spf13/cobra"
)

func newStatus(service link.Service, completions stack.Completions, presentation Presentation) *cobra.Command {
	var selection stackOptions
	cmd := &cobra.Command{Use: "status", Short: "Inspect a Graphite stack, its pull requests, and native GitHub membership", Args: cobra.NoArgs}
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
	selection.register(cmd, completions, "Graphite-tracked local branch to inspect (defaults to current branch)", "Graphite-declared trunk to use as the base")
	return cmd
}

func statusView(plan link.Plan) stackView {
	issues := map[string]string{}
	for _, issue := range plan.Issues {
		issues[issue.Branch] = issue.Reason
	}
	native := githubstack.AssessMembership(plan.Branches, plan.PullRequests)

	view := stackView{
		Operation:    "status",
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
		if marker := nativeMarker(native, branch); marker != "" {
			node.State, node.Severity = marker, nativeSeverity(native.State)
		}
		view.Nodes = append(view.Nodes, node)
	}

	if len(plan.Issues) != 0 {
		return view.block("Safe next action: repair the marked PR mappings.")
	}
	return view.note(nativeMessage(native), nativeNoteSeverity(native.State))
}

func writeStatus(writer io.Writer, plan link.Plan, p Presentation) error {
	return writeStackView(writer, statusView(plan), p)
}

func nativeMarker(s githubstack.Membership, branch string) string {
	pr := s.Branches[branch]
	switch s.State {
	case githubstack.Partial:
		if pr.StackNumber == 0 {
			return "not linked"
		}
	case githubstack.Conflicting:
		if pr.StackNumber == 0 {
			return "not linked"
		}
		return fmt.Sprintf("stack #%d, position %d", pr.StackNumber, pr.StackPosition)
	}
	return ""
}

func nativeSeverity(state githubstack.MembershipState) severity {
	if state == githubstack.Conflicting {
		return severityBad
	}
	return severityWarn
}

func nativeNoteSeverity(state githubstack.MembershipState) severity {
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

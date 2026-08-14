package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/shhac/gt2gh/internal/githubstack"
	"github.com/shhac/gt2gh/internal/link"
	"github.com/spf13/cobra"
)

func newStatus(service link.Service, presentation Presentation) *cobra.Command {
	var selection stackOptions
	cmd := &cobra.Command{Use: "status", Short: "Inspect a Graphite stack, its pull requests, and native GitHub membership", Args: cobra.NoArgs}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		ctx, cancel := context.WithTimeout(cmd.Context(), linkTimeout)
		defer cancel()
		ctx = commandContext(cmd, "status", "read_only", selection.branch, selection.trunk)
		plan, err := service.PlanWithOptions(ctx, selection.Selection())
		if err != nil {
			return err
		}
		return writeStatus(cmd.OutOrStdout(), plan, presentation)
	}
	selection.register(cmd, service, "Graphite-tracked local branch to inspect (defaults to current branch)", "Graphite-declared trunk to use as the base")
	return cmd
}

func writeStatus(w interface{ Write([]byte) (int, error) }, plan link.Plan, p Presentation) error {
	if _, err := fmt.Fprintf(w, "%s: %s\n\n", p.accent("Target"), p.branch(plan.Target)); err != nil {
		return err
	}
	issues := map[string]string{}
	for _, issue := range plan.Issues {
		issues[issue.Branch] = issue.Reason
	}
	native := githubstack.AssessMembership(plan.Branches, plan.PullRequests)
	if _, err := fmt.Fprintf(w, "  %s\n", p.trunk(plan.Base+" (trunk)")); err != nil {
		return err
	}
	for i, branch := range plan.Branches {
		state := p.aligned("[aligned]")
		label := p.branch(branch)
		if reason := issues[branch]; reason != "" {
			state = p.problem("[blocked: " + reason + "]")
		} else {
			label += " (" + p.pr(fmt.Sprintf("#%d", native.Branches[branch].Number)) + ")"
			if marker := nativeMarker(native, branch); marker != "" {
				state += " " + styleNativeMarker(p, native.State, marker)
			}
		}
		if _, err := fmt.Fprintf(w, "%s└─ %s %s\n", strings.Repeat("  ", i+1), label, state); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if len(plan.Issues) != 0 {
		_, err := fmt.Fprintln(w, p.subdued("Safe next action: repair the marked PR mappings."))
		return err
	}
	_, err := fmt.Fprintln(w, nativeMessage(p, native))
	return err
}

func nativeMarker(s githubstack.Membership, branch string) string {
	pr := s.Branches[branch]
	switch s.State {
	case githubstack.Partial:
		if pr.StackNumber == 0 {
			return "[not linked]"
		}
	case githubstack.Conflicting:
		if pr.StackNumber == 0 {
			return "[not linked]"
		}
		return fmt.Sprintf("[stack #%d, position %d]", pr.StackNumber, pr.StackPosition)
	}
	return ""
}

func styleNativeMarker(p Presentation, state githubstack.MembershipState, marker string) string {
	if state == githubstack.Conflicting {
		return p.problem(marker)
	}
	return p.divergent(marker)
}

func nativeMessage(p Presentation, s githubstack.Membership) string {
	switch s.State {
	case githubstack.Aligned:
		return p.subdued(fmt.Sprintf("GitHub stack #%d · selected path %d/%d · aligned", s.StackNumber, s.Selected, s.StackSize))
	case githubstack.Partial:
		return p.subdued(fmt.Sprintf("GitHub stack #%d · partial (%d/%d linked) · run g2g link to add the marked PRs.", s.StackNumber, s.Linked, s.Selected))
	case githubstack.Conflicting:
		return p.problem("GitHub stack: conflicting membership · review the marked PRs before changing anything.")
	default:
		return p.subdued("GitHub stack: not linked · run g2g link to preview a link.")
	}
}

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
	var branch, trunk string
	var noStack bool
	cmd := &cobra.Command{Use: "status", Short: "Inspect a Graphite stack, its pull requests, and native GitHub membership", Args: cobra.NoArgs}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		ctx, cancel := context.WithTimeout(cmd.Context(), linkTimeout)
		defer cancel()
		ctx = commandContext(cmd, "status", "read_only", branch, trunk)
		plan, err := service.PlanWithOptions(ctx, link.Selection{Branch: branch, Trunk: trunk, NoStack: noStack})
		if err != nil {
			return err
		}
		return writeStatus(cmd.OutOrStdout(), plan, presentation)
	}
	cmd.Flags().StringVar(&branch, "branch", "", "Graphite-tracked local branch to inspect (defaults to current branch)")
	cmd.Flags().StringVar(&trunk, "trunk", "", "Graphite-declared trunk to use as the base")
	cmd.Flags().BoolVar(&noStack, "no-stack", false, "stop at the selected branch instead of resolving the full linear stack")
	_ = cmd.RegisterFlagCompletionFunc("branch", completionCallback(service.BranchCompletions))
	_ = cmd.RegisterFlagCompletionFunc("trunk", completionCallback(func(ctx context.Context, prefix string) ([]string, error) {
		return service.TrunkCompletions(ctx, branch, prefix)
	}))
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
	prs := map[string]githubstack.PullRequest{}
	for _, pr := range plan.PullRequests {
		prs[pr.Head] = pr
	}
	native := summarizeNativeStack(plan)
	if _, err := fmt.Fprintf(w, "  %s\n", p.trunk(plan.Base+" (trunk)")); err != nil {
		return err
	}
	for i, branch := range plan.Branches {
		state := p.aligned("[aligned]")
		label := p.branch(branch)
		if reason := issues[branch]; reason != "" {
			state = p.problem("[blocked: " + reason + "]")
		} else {
			label += " (" + p.pr(fmt.Sprintf("#%d", prs[branch].Number)) + ")"
			if marker := native.marker(branch); marker != "" {
				state += " " + native.styleMarker(p, marker)
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
	_, err := fmt.Fprintln(w, native.message(p))
	return err
}

type nativeStackSummary struct {
	branches    map[string]githubstack.PullRequest
	stackNumber int
	stackSize   int
	selected    int
	linked      int
	state       nativeStackState
}

type nativeStackState int

const (
	nativeUnlinked nativeStackState = iota
	nativeAligned
	nativePartial
	nativeConflict
)

func summarizeNativeStack(plan link.Plan) nativeStackSummary {
	prs := make(map[string]githubstack.PullRequest, len(plan.PullRequests))
	for _, pr := range plan.PullRequests {
		prs[pr.Head] = pr
	}
	if len(plan.Issues) != 0 {
		return nativeStackSummary{branches: prs, selected: len(plan.Branches)}
	}
	summary := nativeStackSummary{branches: prs, selected: len(plan.Branches)}
	for index, branch := range plan.Branches {
		pr := prs[branch]
		if pr.StackNumber == 0 {
			continue
		}
		summary.linked++
		if summary.stackNumber == 0 {
			summary.stackNumber = pr.StackNumber
			summary.stackSize = pr.StackSize
		} else if pr.StackNumber != summary.stackNumber || pr.StackSize != summary.stackSize {
			summary.state = nativeConflict
			return summary
		}
		if pr.StackPosition != index+1 {
			summary.state = nativeConflict
			return summary
		}
	}
	if summary.linked == 0 {
		summary.state = nativeUnlinked
		return summary
	}
	if summary.linked != summary.selected {
		summary.state = nativePartial
		return summary
	}
	summary.state = nativeAligned
	return summary
}

func (s nativeStackSummary) marker(branch string) string {
	pr := s.branches[branch]
	switch s.state {
	case nativePartial:
		if pr.StackNumber == 0 {
			return "[not linked]"
		}
	case nativeConflict:
		if pr.StackNumber == 0 {
			return "[not linked]"
		}
		return fmt.Sprintf("[stack #%d, position %d]", pr.StackNumber, pr.StackPosition)
	}
	return ""
}

func (s nativeStackSummary) styleMarker(p Presentation, marker string) string {
	if s.state == nativeConflict {
		return p.problem(marker)
	}
	return p.divergent(marker)
}

func (s nativeStackSummary) message(p Presentation) string {
	switch s.state {
	case nativeAligned:
		return p.subdued(fmt.Sprintf("GitHub stack #%d · selected path %d/%d · aligned", s.stackNumber, s.selected, s.stackSize))
	case nativePartial:
		return p.subdued(fmt.Sprintf("GitHub stack #%d · partial (%d/%d linked) · run g2g link to add the marked PRs.", s.stackNumber, s.linked, s.selected))
	case nativeConflict:
		return p.problem("GitHub stack: conflicting membership · review the marked PRs before changing anything.")
	default:
		return p.subdued("GitHub stack: not linked · run g2g link to preview a link.")
	}
}

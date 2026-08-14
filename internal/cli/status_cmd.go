package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/shhac/gt2gh/internal/link"
	"github.com/spf13/cobra"
)

func newStatus(service link.Service, presentation Presentation) *cobra.Command {
	var branch, trunk string
	var noStack bool
	cmd := &cobra.Command{Use: "status", Short: "Inspect a Graphite stack and its GitHub pull requests", Args: cobra.NoArgs}
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
	prs := map[string]int{}
	for _, pr := range plan.PullRequests {
		prs[pr.Head] = pr.Number
	}
	if _, err := fmt.Fprintf(w, "  %s\n", p.trunk(plan.Base+" (trunk)")); err != nil {
		return err
	}
	for i, branch := range plan.Branches {
		state := p.aligned("[aligned]")
		label := p.branch(branch)
		if reason := issues[branch]; reason != "" {
			state = p.problem("[blocked: " + reason + "]")
		} else {
			label += " (" + p.pr(fmt.Sprintf("#%d", prs[branch])) + ")"
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
	_, err := fmt.Fprintln(w, p.subdued("GitHub native-stack membership is not inspected yet; use link to create or update it, or unlink --stack-number for explicit recovery."))
	return err
}

package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shhac/gt2gh/internal/link"
	syncer "github.com/shhac/gt2gh/internal/sync"
)

func newSync(service syncer.Service, linkService link.Service) *cobra.Command {
	var branch string
	var apply bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Reconcile GitHub's native stack to Graphite (preview by default)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), linkTimeout)
			defer cancel()
			plan, err := service.Preview(ctx, branch)
			if err != nil {
				return err
			}
			writeSyncPreview(cmd.OutOrStdout(), plan)
			if !apply {
				fmt.Fprintln(cmd.OutOrStdout(), "Preview only: rerun with --apply to reconcile eligible GitHub stack relationships.")
				return nil
			}
			if _, err := service.Apply(ctx, branch, plan); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Applied: GitHub native stack reconciliation completed after revalidation.")
			return nil
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "", "Graphite-tracked local branch to reconcile (defaults to current branch)")
	cmd.Flags().BoolVar(&apply, "apply", false, "reconcile eligible GitHub stack relationships after revalidation")
	_ = cmd.RegisterFlagCompletionFunc("branch", func(_ *cobra.Command, _ []string, prefix string) ([]string, cobra.ShellCompDirective) {
		ctx, cancel := context.WithTimeout(context.Background(), completionTimeout)
		defer cancel()
		branches, err := linkService.BranchCompletions(ctx, prefix)
		if err != nil {
			return nil, cobra.ShellCompDirectiveError | cobra.ShellCompDirectiveNoFileComp
		}
		return branches, cobra.ShellCompDirectiveNoFileComp
	})
	return cmd
}

func writeSyncPreview(writer io.Writer, plan syncer.Plan) {
	fmt.Fprintf(writer, "Resolved target (%s): %s\n", plan.Link.TargetSource, plan.Link.Target)
	fmt.Fprintf(writer, "Declared Graphite trunk: %s\n", plan.Link.Trunk)
	fmt.Fprintf(writer, "Graphite path (bottom to top): %s\n", strings.Join(plan.Link.Branches, " -> "))
	fmt.Fprintln(writer, "GitHub reconciliation:")
	for _, item := range plan.Items {
		if item.State == syncer.Aligned {
			fmt.Fprintf(writer, "  %s: aligned (base %s)\n", item.Branch, item.ExpectedBase)
			continue
		}
		fmt.Fprintf(writer, "  %s: %s (%s)\n", item.Branch, item.State, formatSyncItem(item))
	}
	fmt.Fprintf(writer, "Reconciliation summary: %s\n", plan.Summary())
	fmt.Fprintf(writer, "Proposed command: gh stack link --base %s %s\n", plan.Link.Trunk, strings.Join(plan.Link.Branches, " "))
	if !plan.CanApply() {
		fmt.Fprintln(writer, "Apply blocked: every path branch must already have one open GitHub pull request.")
	}
}

func formatSyncItem(item syncer.Item) string {
	if item.PullRequest == nil {
		return "no PR"
	}
	if item.State == syncer.Divergent {
		return fmt.Sprintf("PR #%d base %s, want %s", item.PullRequest.Number, item.PullRequest.Base, item.ExpectedBase)
	}
	return fmt.Sprintf("PR #%d is %s", item.PullRequest.Number, strings.ToLower(item.PullRequest.State))
}

package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/shhac/gt2gh/internal/link"
	syncer "github.com/shhac/gt2gh/internal/sync"
)

func newSync(service syncer.Service, linkService link.Service, presentation Presentation) *cobra.Command {
	var branch string
	var trunk string
	var noStack bool
	var apply bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Reconcile GitHub's native stack to Graphite (preview by default)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), linkTimeout)
			defer cancel()
			mode := "preview"
			if apply {
				mode = "apply"
			}
			ctx = commandContext(cmd, "sync", mode, branch, trunk)
			selection := link.Selection{Branch: branch, Trunk: trunk, NoStack: noStack}
			plan, err := service.PreviewWithOptions(ctx, selection)
			if err != nil {
				return err
			}
			if !apply {
				if err := writeSyncPlan(cmd.OutOrStdout(), plan, presentation); err != nil {
					return err
				}
				if _, err := fmt.Fprintln(cmd.OutOrStdout()); err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), presentation.notice("No changes were made.")+" --apply re-discovers and revalidates before invoking gh stack link.")
				return nil
			}
			validated, err := service.RevalidateWithOptions(ctx, selection, plan)
			if err != nil {
				writeNotApplied(cmd.OutOrStdout(), presentation, err)
				return err
			}
			if validated.NothingToSync() {
				if err := writeSyncPlan(cmd.OutOrStdout(), validated, presentation); err != nil {
					return err
				}
				if _, err := fmt.Fprintln(cmd.OutOrStdout()); err != nil {
					return err
				}
				_, err := fmt.Fprintln(cmd.OutOrStdout(), presentation.notice("No changes were needed or made."))
				return err
			}
			if err := writeReadyToSync(cmd.OutOrStdout(), validated, presentation); err != nil {
				writeNotApplied(cmd.OutOrStdout(), presentation, err)
				return fmt.Errorf("render ready-to-apply output: %w", err)
			}
			if err := flushOutput(cmd.OutOrStdout()); err != nil {
				writeNotApplied(cmd.OutOrStdout(), presentation, err)
				return err
			}
			if err := service.Execute(ctx, validated); err != nil {
				writeNotApplied(cmd.OutOrStdout(), presentation, err)
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout())
			fmt.Fprintln(cmd.OutOrStdout(), presentation.notice("Applied — GitHub stack updated"))
			fmt.Fprintln(cmd.OutOrStdout(), presentation.subdued("Changes were made."))
			return nil
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "", "Graphite-tracked local branch to reconcile (defaults to current branch)")
	cmd.Flags().StringVar(&trunk, "trunk", "", "Graphite-declared trunk to use as the link base")
	cmd.Flags().BoolVar(&noStack, "no-stack", false, "stop at the selected branch instead of resolving the full linear stack")
	cmd.Flags().BoolVar(&apply, "apply", false, "reconcile eligible GitHub stack relationships after revalidation")
	_ = cmd.RegisterFlagCompletionFunc("branch", completionCallback(linkService.BranchCompletions))
	_ = cmd.RegisterFlagCompletionFunc("trunk", completionCallback(func(ctx context.Context, prefix string) ([]string, error) {
		return linkService.TrunkCompletions(ctx, branch, prefix)
	}))
	return cmd
}

package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/shhac/gt2gh/internal/link"
	syncer "github.com/shhac/gt2gh/internal/sync"
)

func newSync(service syncer.Service, linkService link.Service, presentation Presentation) *cobra.Command {
	var selection stackOptions
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
			ctx = commandContext(cmd, "sync", mode, selection.branch, selection.trunk)
			plan, err := service.PreviewWithOptions(ctx, selection.Selection())
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
			validated, err := service.RevalidateWithOptions(ctx, selection.Selection(), plan)
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
	selection.register(cmd, linkService, "Graphite-tracked local branch to reconcile (defaults to current branch)", "Graphite-declared trunk to use as the link base")
	cmd.Flags().BoolVar(&apply, "apply", false, "reconcile eligible GitHub stack relationships after revalidation")
	return cmd
}

package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/shhac/gt2gh/internal/stack"
	syncer "github.com/shhac/gt2gh/internal/sync"
)

func newSync(service syncer.Service, completions stack.Completions, presentation Presentation) *cobra.Command {
	var selection stackOptions
	var apply bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Reconcile GitHub's native stack to Graphite (preview by default)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			presentation := presentation.resolve(cmd)
			mode := "preview"
			if apply {
				mode = "apply"
			}
			budgets := newBudgets(cmd)
			root := commandContext(cmd.Context(), cmd, "sync", mode, selection.branch, selection.trunk)
			ctx, cancel := budgets.discovery(root)
			defer cancel()
			plan, err := service.Preview(ctx, selection.Selection())
			if err != nil {
				return err
			}
			if !apply {
				if err := writeSyncPlan(cmd.OutOrStdout(), plan, presentation); err != nil {
					return err
				}
				prose(cmd.OutOrStdout(), presentation, "\n"+presentation.notice("No changes were made.")+" Re-run with --apply to reconcile.")
				return nil
			}
			validated, err := service.Revalidate(ctx, selection.Selection(), plan)
			if err != nil {
				return writeNotApplied(cmd.OutOrStdout(), presentation, err)
			}
			if validated.NothingToSync() {
				if err := writeSyncPlan(cmd.OutOrStdout(), validated, presentation); err != nil {
					return err
				}
				err := prose(cmd.OutOrStdout(), presentation, "\n"+presentation.notice("No changes were needed or made."))
				return err
			}
			if err := writeReadyToSync(cmd.OutOrStdout(), validated, presentation); err != nil {
				return fmt.Errorf("render ready-to-apply output: %w", writeNotApplied(cmd.OutOrStdout(), presentation, err))
			}
			if err := flushOutput(cmd.OutOrStdout()); err != nil {
				return writeNotApplied(cmd.OutOrStdout(), presentation, err)
			}
			mutateCtx, cancelMutation := budgets.mutation(root, len(validated.Link.Branches))
			defer cancelMutation()
			if err := service.Execute(mutateCtx, validated); err != nil {
				return writeNotApplied(cmd.OutOrStdout(), presentation, mutationTimeout(err, "Run g2g status to see whether GitHub recorded the link."))
			}
			prose(cmd.OutOrStdout(), presentation, presentation.notice("Applied — GitHub stack updated"))
			prose(cmd.OutOrStdout(), presentation, presentation.subdued("Changes were made."))
			return nil
		},
	}
	selection.register(cmd, completions, "Graphite-tracked local branch to reconcile (defaults to current branch)", "Graphite-declared trunk to use as the link base")
	cmd.Flags().BoolVar(&apply, "apply", false, "reconcile eligible GitHub stack relationships after revalidation")
	return cmd
}

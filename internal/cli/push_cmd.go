package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/shhac/gt2gh/internal/push"
	"github.com/shhac/gt2gh/internal/stack"
)

func newPush(service push.Service, completions stack.Completions, presentation Presentation) *cobra.Command {
	var remote string
	var selection stackOptions
	var apply bool
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Atomically push a Graphite stack's local refs (preview by default)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			presentation := presentation.resolve(cmd)
			mode := "preview"
			if apply {
				mode = "apply"
			}
			budgets := newBudgets(cmd)
			root := commandContext(cmd.Context(), cmd, "push", mode, selection.branch, selection.trunk)
			ctx, cancel := budgets.discovery(root)
			defer cancel()
			plan, err := service.Plan(ctx, selection.Selection(), remote)
			if err != nil {
				return err
			}
			if !apply {
				if err := writePushPlan(cmd.OutOrStdout(), plan, presentation); err != nil {
					return err
				}
				err := prose(cmd.OutOrStdout(), presentation, "\n"+presentation.notice("No changes were made.")+" Re-run with --apply to push.")
				return err
			}
			validated, err := service.Revalidate(ctx, selection.Selection(), remote, plan)
			if err != nil {
				return writeNotApplied(cmd.OutOrStdout(), presentation, err)
			}
			if err := writeReadyToPush(cmd.OutOrStdout(), validated, presentation); err != nil {
				return fmt.Errorf("render ready-to-apply output: %w", writeNotApplied(cmd.OutOrStdout(), presentation, err))
			}
			if err := flushOutput(cmd.OutOrStdout()); err != nil {
				return writeNotApplied(cmd.OutOrStdout(), presentation, err)
			}
			mutateCtx, cancelMutation := budgets.mutation(root, len(validated.Branches))
			defer cancelMutation()
			if err := service.Execute(mutateCtx, validated); err != nil {
				return writeNotApplied(cmd.OutOrStdout(), presentation, mutationTimeout(err, "The push is atomic, so every selected ref advanced or none did; re-run g2g push to see which."))
			}
			prose(cmd.OutOrStdout(), presentation, "\n"+presentation.notice("Applied — remote refs updated atomically"))
			prose(cmd.OutOrStdout(), presentation, presentation.subdued("Changes were made."))
			return nil
		},
	}
	selection.register(cmd, completions, "Graphite-tracked local branch to push (defaults to current branch)", "Graphite-declared trunk to use as the push base")
	cmd.Flags().StringVar(&remote, "remote", "origin", "Git remote to push to")
	cmd.Flags().BoolVar(&apply, "apply", false, "atomically push with --force-with-lease after revalidation")
	return cmd
}

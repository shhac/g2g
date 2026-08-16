package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/shhac/gt2gh/internal/align"
)

func newMirror(service align.Service, guard func(context.Context) error, presentation Presentation) *cobra.Command {
	var prune bool
	var apply bool
	cmd := &cobra.Command{
		Use:   "mirror",
		Short: "Make Graphite agree with the gt2gh graph",
		Long: "Reconciles Graphite so it records what the gt2gh graph records.\n\n" +
			"Nothing is ever removed from the gt2gh graph: this keeps the two in step, it does not hand ownership over. " +
			"g2g keeps answering for every branch it already answered for.",
		Args: cobra.NoArgs,
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		presentation := presentation.resolve(cmd)
		ctx := commandContext(cmd.Context(), cmd, "mirror", applyMode(apply), "", "")
		budgets := newBudgets(cmd)
		if guard != nil {
			if err := guard(ctx); err != nil {
				return err
			}
		}

		discoveryCtx, cancel := budgets.discovery(ctx)
		defer cancel()
		plan, err := service.PlanMirror(discoveryCtx, prune)
		if err != nil {
			return err
		}
		if !apply {
			if err := writeStackView(cmd.OutOrStdout(), mirrorView(plan, prune), presentation); err != nil {
				return err
			}
			return prose(cmd.OutOrStdout(), presentation, "\n"+presentation.notice("No changes were made.")+" Rerun with --apply to align Graphite.")
		}
		if plan.Blocked != "" {
			return writeNotApplied(cmd.OutOrStdout(), presentation, fmt.Errorf("%s", plan.Blocked))
		}
		return applyMirror(cmd, ctx, budgets, service, plan, prune, presentation)
	}
	cmd.Flags().BoolVar(&prune, "prune", false, "untrack branches in Graphite that the gt2gh graph does not record (the gt2gh graph is never changed)")
	cmd.Flags().BoolVar(&apply, "apply", false, "perform the reconciliation instead of previewing it")
	return cmd
}

// applyMirror re-reads both graphs, renders, and flushes before it writes.
func applyMirror(cmd *cobra.Command, ctx context.Context, budgets budgets, service align.Service, plan align.MirrorPlan, prune bool, p Presentation) error {
	mutateCtx, cancel := budgets.mutation(ctx, len(plan.Writes)+len(plan.Prunes))
	defer cancel()

	confirmed, err := service.RevalidateMirror(mutateCtx, prune, plan)
	if err != nil {
		return writeNotApplied(cmd.OutOrStdout(), p, err)
	}
	if err := writeReadyBanner(cmd.OutOrStdout(), p); err != nil {
		return err
	}
	if err := writeStackView(cmd.OutOrStdout(), mirrorView(confirmed, prune), p); err != nil {
		return err
	}
	if err := flushOutput(cmd.OutOrStdout()); err != nil {
		return writeNotApplied(cmd.OutOrStdout(), p, err)
	}

	if err := service.ApplyMirror(mutateCtx, confirmed); err != nil {
		return writeNotApplied(cmd.OutOrStdout(), p, err)
	}
	_ = prose(cmd.OutOrStdout(), p, p.notice("Graphite is aligned."))
	return prose(cmd.OutOrStdout(), p, p.subdued("The gt2gh graph is unchanged and still answers for these branches."))
}

package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/shhac/gt2gh/internal/align"
)

func newImport(service align.Service, guard func(context.Context) error, presentation Presentation) *cobra.Command {
	var apply bool
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Adopt the branches Graphite declares into the gt2gh graph",
		Long: "Records what Graphite declares, so gt2gh can answer for those branches and restack them.\n\n" +
			"Adoption is the authority claim, so gt2gh answers for every branch this adopts from then on, " +
			"and --from graphite becomes the only way to see Graphite's view of them. " +
			"Nothing is written to Graphite and nothing is removed from either record.",
		Args: cobra.NoArgs,
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		presentation := presentation.resolve(cmd)
		ctx := commandContext(cmd.Context(), cmd, "import", applyMode(apply), "", "")
		budgets := newBudgets(cmd)
		if guard != nil {
			if err := guard(ctx); err != nil {
				return err
			}
		}

		discoveryCtx, cancel := budgets.discovery(ctx)
		defer cancel()
		plan, err := service.PlanImport(discoveryCtx)
		if err != nil {
			return err
		}
		if !apply {
			if err := writeStackView(cmd.OutOrStdout(), importView(plan), presentation); err != nil {
				return err
			}
			return prose(cmd.OutOrStdout(), presentation, "\n"+presentation.notice("No changes were made.")+" Rerun with --apply to adopt them.")
		}
		if plan.Blocked != "" {
			return writeNotApplied(cmd.OutOrStdout(), presentation, fmt.Errorf("%s", plan.Blocked))
		}
		return applyImport(cmd, ctx, budgets, service, plan, presentation)
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "record the adoptions instead of previewing them")
	return cmd
}

// applyImport re-reads both graphs, renders, and flushes before it writes.
func applyImport(cmd *cobra.Command, ctx context.Context, budgets budgets, service align.Service, plan align.ImportPlan, p Presentation) error {
	mutateCtx, cancel := budgets.mutation(ctx, len(plan.Adopt))
	defer cancel()

	confirmed, err := service.RevalidateImport(mutateCtx, plan)
	if err != nil {
		return writeNotApplied(cmd.OutOrStdout(), p, err)
	}
	if err := writeReadyBanner(cmd.OutOrStdout(), p); err != nil {
		return err
	}
	if err := writeStackView(cmd.OutOrStdout(), importView(confirmed), p); err != nil {
		return err
	}
	if err := flushOutput(cmd.OutOrStdout()); err != nil {
		return writeNotApplied(cmd.OutOrStdout(), p, err)
	}

	if err := service.ApplyImport(mutateCtx, confirmed); err != nil {
		return writeNotApplied(cmd.OutOrStdout(), p, err)
	}
	_ = prose(cmd.OutOrStdout(), p, p.notice("Adopted."))
	return prose(cmd.OutOrStdout(), p, p.subdued("Graphite still tracks these branches; gt2gh is what answers for them now."))
}

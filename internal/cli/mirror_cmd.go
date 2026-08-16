package cli

import (
	"context"
	"io"

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
		flow := applyFlow[align.MirrorPlan]{
			plan: func(ctx context.Context) (align.MirrorPlan, error) {
				return service.PlanMirror(ctx, prune)
			},
			revalidate: func(ctx context.Context, preview align.MirrorPlan) (align.MirrorPlan, error) {
				return service.RevalidateMirror(ctx, prune, preview)
			},
			render: func(writer io.Writer, plan align.MirrorPlan, p Presentation) error {
				return writeStackView(writer, mirrorView(plan, prune), p)
			},
			guard:    guard,
			execute:  service.ApplyMirror,
			branches: func(plan align.MirrorPlan) int { return len(plan.Writes) + len(plan.Prunes) },
			noOp:     mirrorIsNoOp,
			notices: flowNotices{
				preview:  "Rerun with --apply to align Graphite.",
				noOp:     "Graphite already agrees with the gt2gh graph. Nothing to do.",
				applied:  "Graphite is aligned.",
				changed:  "The gt2gh graph is unchanged and still answers for these branches.",
				recovery: "Some branches may already have been tracked in Graphite · rerun g2g mirror to see what is left.",
			},
		}
		return flow.run(cmd, ctx, newBudgets(cmd), presentation, apply)
	}
	cmd.Flags().BoolVar(&prune, "prune", false, "untrack branches in Graphite that the gt2gh graph does not record (the gt2gh graph is never changed)")
	cmd.Flags().BoolVar(&apply, "apply", false, "perform the reconciliation instead of previewing it")
	return cmd
}

// mirrorIsNoOp reports a plan with nothing to do. A blocked plan also has no
// writes, and must not be reported as agreement: "nothing to do" and "this
// cannot be done" are opposite answers that happen to produce an empty list.
func mirrorIsNoOp(plan align.MirrorPlan) bool { return plan.Blocked == "" && plan.Aligned() }

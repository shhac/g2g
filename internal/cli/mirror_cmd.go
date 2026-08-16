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
		Use:     "mirror",
		GroupID: groupMaintain,
		Short:   "Make Graphite agree with the g2g graph (preview by default)",
		Long: "Reconciles Graphite so it records what the g2g graph records.\n\n" +
			"Nothing is ever removed from the g2g graph: this keeps the two in step, it does not hand ownership over. " +
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
				noOp:     "Graphite already agrees with the g2g graph. Nothing to do.",
				applied:  "Graphite is aligned.",
				changed:  "The g2g graph is unchanged and still answers for these branches.",
				recovery: "Some branches may already have been tracked in Graphite · rerun g2g mirror to see what is left.",
			},
		}
		return flow.run(cmd, ctx, newBudgets(cmd), presentation, apply)
	}
	cmd.Flags().BoolVar(&prune, "prune", false, "also untrack, in Graphite, the branches the g2g graph does not record · off by default, and the g2g graph is never changed")
	cmd.Flags().BoolVar(&apply, "apply", false, "perform the reconciliation instead of previewing it")
	return cmd
}

// mirrorIsNoOp reports a plan with nothing to do. A blocked plan also has no
// writes, and must not be reported as agreement: "nothing to do" and "this
// cannot be done" are opposite answers that happen to produce an empty list.
func mirrorIsNoOp(plan align.MirrorPlan) bool { return plan.Blocked == "" && plan.Aligned() }

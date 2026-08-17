package cli

import (
	"context"
	"io"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/align"
)

func newImport(service align.Service, guard func(context.Context) error, presentation Presentation) *cobra.Command {
	var apply bool
	cmd := &cobra.Command{
		Use:     "import",
		GroupID: groupStructure,
		Short:   "Adopt the branches Graphite declares into the g2g graph (preview by default)",
		Long: "Records what Graphite declares, so g2g can answer for those branches and restack them.\n\n" +
			"Adoption is the authority claim, so g2g answers for every branch this adopts from then on, " +
			"and --from graphite becomes the only way to see Graphite's view of them. " +
			"Nothing is written to Graphite and nothing is removed from either record.",
		Args: cobra.NoArgs,
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		presentation := presentation.resolve(cmd)
		ctx := commandContext(cmd.Context(), cmd, "import", applyMode(apply), "", "")
		flow := applyFlow[align.ImportPlan]{
			plan:       service.PlanImport,
			revalidate: service.RevalidateImport,
			render: func(writer io.Writer, plan align.ImportPlan, p Presentation) error {
				return writeStackView(writer, importView(plan), p)
			},
			guard:    guard,
			execute:  service.ApplyImport,
			branches: func(plan align.ImportPlan) int { return len(plan.Adopt) },
			noOp:     importIsNoOp,
			notices: flowNotices{
				preview:  "Rerun with --apply to adopt them.",
				noOp:     "Graphite declares nothing the g2g graph does not already record. Nothing to do.",
				applied:  "Adopted.",
				changed:  "Graphite still tracks these branches; g2g is what answers for them now.",
				recovery: "The graph store may or may not have been written · rerun g2g import to see what is left.",
			},
		}
		return flow.run(cmd, ctx, newBudgets(cmd), presentation, apply)
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "record the adoptions instead of previewing them")
	return cmd
}

// importIsNoOp reports a plan with nothing to adopt. A blocked plan adopts
// nothing either, and must not be reported as agreement.
func importIsNoOp(plan align.ImportPlan) bool { return plan.Blocked == "" && len(plan.Adopt) == 0 }

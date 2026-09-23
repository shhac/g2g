package cli

import (
	"context"
	"io"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/graph"
)

// newAdopt records a stack that already exists, from git alone: the user names
// the trunk, or it is the one recorded root on the ancestry, and commit
// ancestry supplies the rest. It refuses rather than guessing wherever
// ancestry cannot order two branches. Graphite's and GitHub's records have
// their own adopt, under their own names.
func newAdopt(service graph.Service, guard func(context.Context) error, presentation Presentation) *cobra.Command {
	var selection graphOptions
	var trunk string
	var apply bool
	cmd := &cobra.Command{
		Use:     "adopt",
		GroupID: groupShape,
		Short:   "Record the stack you are on, from git's own history (preview by default)",
		Long: "Records a stack that already exists in one step: the order comes from commit ancestry, from the " +
			"trunk up to the selected branch and everything built on it. It records a forest, not a chain — a " +
			"branch that merely shares the trunk is a separate stack and is left alone — and it refuses rather " +
			"than guessing wherever ancestry cannot order two branches.\n\n" +
			"The first time, name the trunk with --trunk; after that the recorded root is used. To adopt what " +
			"another tool declares instead, see g2g graphite adopt and g2g github adopt.",
		Args: cobra.NoArgs,
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		presentation := presentation.resolve(cmd)
		ctx := commandContext(cmd.Context(), cmd, applyMode(apply), selection.branch, trunk)
		return adoptFlow(service, selection, trunk, guard).run(cmd, ctx, newBudgets(cmd), presentation, apply)
	}
	cmd.Flags().StringVar(&trunk, "trunk", "", "where the stack starts (defaults to the only recorded root on the ancestry)")
	_ = cmd.RegisterFlagCompletionFunc("trunk", completionCallback(localBranchCompletions(service)))
	cmd.Flags().BoolVar(&apply, "apply", false, "record the stack instead of previewing it")
	selection.registerBranch(cmd, service)
	return cmd
}

// adoptFlow is the same safety sequence as every other mutating command,
// over the whole-ancestry plan rather than a single edge.
func adoptFlow(service graph.Service, selection graphOptions, trunk string, guard func(context.Context) error) applyFlow[graph.StackPlan] {
	return applyFlow[graph.StackPlan]{
		plan: func(ctx context.Context) (graph.StackPlan, error) {
			return service.PlanStack(ctx, selection.Selection(), trunk)
		},
		revalidate: func(ctx context.Context, preview graph.StackPlan) (graph.StackPlan, error) {
			return service.RevalidateStack(ctx, selection.Selection(), trunk, preview)
		},
		render: func(writer io.Writer, plan graph.StackPlan, p Presentation) error {
			return writeGraphView(writer, gitAdoptView(plan), plan.Discovery, p)
		},
		guard:    guard,
		execute:  service.ApplyStack,
		branches: func(plan graph.StackPlan) int { return len(plan.Record) },
		noOp:     func(plan graph.StackPlan) bool { return plan.NoOp() },
		blocked:  func(plan graph.StackPlan) string { return plan.Blocked },
		notices: flowNotices{
			preview:       "Rerun with --apply to record this stack.",
			noOp:          "The graph already records this whole ancestry. Nothing to do.",
			applied:       "Recorded.",
			changed:       "The g2g-owned graph now records this stack.",
			recovery:      "The graph store may or may not have been written.",
			suggestedNext: "g2g status",
		},
	}
}

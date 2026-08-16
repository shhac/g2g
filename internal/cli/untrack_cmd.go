package cli

import (
	"context"
	"io"

	"github.com/spf13/cobra"

	"github.com/shhac/gt2gh/internal/graph"
)

func newUntrack(service graph.Service, guard func(context.Context) error, presentation Presentation) *cobra.Command {
	var selection graphOptions
	var apply bool
	cmd := &cobra.Command{Use: "untrack", GroupID: groupStructure, Short: "Remove a branch from the g2g-owned graph (preview by default)", Args: cobra.NoArgs}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		presentation := presentation.resolve(cmd)
		ctx := commandContext(cmd.Context(), cmd, "untrack", applyMode(apply), selection.branch, "")
		flow := applyFlow[graph.UntrackPlan]{
			plan: func(ctx context.Context) (graph.UntrackPlan, error) {
				return service.PlanUntrack(ctx, selection.Selection())
			},
			revalidate: func(ctx context.Context, preview graph.UntrackPlan) (graph.UntrackPlan, error) {
				return service.RevalidateUntrack(ctx, selection.Selection(), preview)
			},
			render: func(writer io.Writer, plan graph.UntrackPlan, p Presentation) error {
				return writeGraphView(writer, untrackView(plan), plan.Discovery, p)
			},
			guard:    guard,
			execute:  service.ApplyUntrack,
			branches: func(plan graph.UntrackPlan) int { return len(plan.Removed) },
			noOp:     func(plan graph.UntrackPlan) bool { return len(plan.Removed) == 0 },
			notices: flowNotices{
				preview:  "Rerun with --apply to remove these edges.",
				noOp:     "No selected branch is tracked. Nothing to do.",
				applied:  "Removed.",
				changed:  "The g2g-owned graph no longer records these parents.",
				recovery: "The graph store may or may not have been written.",
			},
		}
		return flow.run(cmd, ctx, newBudgets(cmd), presentation, apply)
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "remove the recorded parents instead of previewing the removal")
	selection.registerBranch(cmd, service)
	selection.registerScope(cmd, []graph.Scope{graph.ScopeBranch, graph.ScopeSubtree}, "how much to remove: branch or subtree")
	return cmd
}

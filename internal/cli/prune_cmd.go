package cli

import (
	"context"
	"io"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/prune"
	"github.com/shhac/g2g/internal/shape"
)

func newPrune(service prune.Service, guard func(context.Context) error, presentation Presentation) *cobra.Command {
	var selection graphOptions
	var apply bool
	cmd := &cobra.Command{
		Use:     "prune",
		GroupID: groupMaintain,
		Short:   "Forget branches whose work has landed, in the g2g graph only (preview by default)",
		Args:    cobra.NoArgs,
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		presentation := presentation.resolve(cmd)
		if err := selection.validateScope(); err != nil {
			return err
		}
		ctx := commandContext(cmd.Context(), cmd, "prune", applyMode(apply), selection.branch, "")
		flow := applyFlow[prune.Plan]{
			guard: guard,
			plan:  func(ctx context.Context) (prune.Plan, error) { return service.Plan(ctx, selection.Selection()) },
			revalidate: func(ctx context.Context, preview prune.Plan) (prune.Plan, error) {
				return service.Revalidate(ctx, selection.Selection(), preview)
			},
			render:   func(w io.Writer, plan prune.Plan, p Presentation) error { return writePrunePlan(w, plan, p) },
			execute:  func(ctx context.Context, plan prune.Plan) error { return service.Apply(ctx, plan) },
			branches: func(plan prune.Plan) int { return len(plan.Landed) },
			noOp:     func(plan prune.Plan) bool { return plan.Nothing() },
			// Forgetting a branch while something recorded under it survives is
			// a refusal, so it belongs before the ready banner rather than
			// after it, in Apply.
			blocked: func(plan prune.Plan) string { return plan.Blocked },
			notices: flowNotices{
				preview:       "Rerun with --apply to forget them.",
				noOp:          "Nothing has landed.",
				applied:       "Forgotten.",
				changed:       "The graph no longer records them. No branch was deleted.",
				suggestedNext: "g2g graph",
			},
		}
		return flow.run(cmd, ctx, newBudgets(cmd), presentation, apply)
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "edit the graph instead of previewing the change")
	selection.registerBranch(cmd, service.Graph)
	// Pruning edits the record and deletes nothing, so it can range as wide as
	// a read. It defaults to the stack being worked on, which is the boundary
	// sync uses, because "what has landed" is asked about a stack rather than
	// about a repository.
	selection.registerScope(cmd, shape.ReadScopes, graph.ScopeStack, scopeUsage("forget", shape.ReadScopes))
	return cmd
}

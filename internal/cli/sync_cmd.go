package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/shhac/gt2gh/internal/stack"
	syncer "github.com/shhac/gt2gh/internal/sync"
)

func newSync(service syncer.Service, completions stack.Completions, guard func(context.Context) error, presentation Presentation) *cobra.Command {
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
			root := commandContext(cmd.Context(), cmd, "sync", mode, selection.branch, selection.trunk)
			flow := applyFlow[syncer.Plan]{
				plan: func(ctx context.Context) (syncer.Plan, error) { return service.Preview(ctx, selection.Selection()) },
				revalidate: func(ctx context.Context, preview syncer.Plan) (syncer.Plan, error) {
					return service.Revalidate(ctx, selection.Selection(), preview)
				},
				render:   writeSyncPlan,
				guard:    guard,
				execute:  service.Execute,
				branches: func(plan syncer.Plan) int { return len(plan.Discovery.Branches) },
				noOp:     syncer.Plan.NothingToSync,
				notices: flowNotices{
					preview:  "Re-run with --apply to reconcile.",
					noOp:     "No changes were needed or made.",
					applied:  "Applied — GitHub stack updated",
					changed:  "Changes were made.",
					recovery: "Run g2g status to see whether GitHub recorded the link.",
				},
			}
			return flow.run(cmd, root, newBudgets(cmd), presentation, apply)
		},
	}
	selection.register(cmd, completions, "Graphite-tracked local branch to reconcile (defaults to current branch)", "Graphite-declared trunk to use as the link base")
	cmd.Flags().BoolVar(&apply, "apply", false, "reconcile eligible GitHub stack relationships after revalidation")
	return cmd
}

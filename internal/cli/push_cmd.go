package cli

import (
	"context"

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
			root := commandContext(cmd.Context(), cmd, "push", mode, selection.branch, selection.trunk)
			flow := applyFlow[push.Plan]{
				plan: func(ctx context.Context) (push.Plan, error) { return service.Plan(ctx, selection.Selection(), remote) },
				revalidate: func(ctx context.Context, preview push.Plan) (push.Plan, error) {
					return service.Revalidate(ctx, selection.Selection(), remote, preview)
				},
				render:   writePushPlan,
				execute:  service.Execute,
				branches: func(plan push.Plan) int { return len(plan.Branches) },
				notices: flowNotices{
					preview:  "Re-run with --apply to push.",
					applied:  "Applied — remote refs updated atomically",
					changed:  "Changes were made.",
					recovery: "The push is atomic, so every selected ref advanced or none did; re-run g2g push to see which.",
				},
			}
			return flow.run(cmd, root, newBudgets(cmd), presentation, apply)
		},
	}
	selection.register(cmd, completions, "Graphite-tracked local branch to push (defaults to current branch)", "Graphite-declared trunk to use as the push base")
	cmd.Flags().StringVar(&remote, "remote", "origin", "Git remote to push to")
	cmd.Flags().BoolVar(&apply, "apply", false, "atomically push with --force-with-lease after revalidation")
	return cmd
}

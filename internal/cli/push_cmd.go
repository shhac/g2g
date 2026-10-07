package cli

import (
	"context"

	"github.com/spf13/cobra"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/push"
	"github.com/shhac/g2g/internal/stack"
)

func newPush(service push.Service, completions stack.Completions, guard func(context.Context) error, presentation Presentation) *cobra.Command {
	var remote string
	var selection stackOptions
	var apply, noSetUpstream bool
	cmd := &cobra.Command{
		Use:     "push",
		GroupID: groupPublish,
		Short:   "Atomically push a stack's local refs (preview by default)",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			presentation := presentation.resolve(cmd)
			if err := selection.validate(); err != nil {
				return err
			}
			root := commandContext(cmd.Context(), cmd, applyMode(apply), selection.branch, selection.trunk)
			upstream := upstreamFor(noSetUpstream)
			flow := applyFlow[push.Plan]{
				plan: func(ctx context.Context) (push.Plan, error) {
					return service.Plan(ctx, selection.Selection(), remote, upstream)
				},
				revalidate: func(ctx context.Context, preview push.Plan) (push.Plan, error) {
					return service.Revalidate(ctx, selection.Selection(), remote, upstream, preview)
				},
				render:   writePushPlan,
				guard:    guard,
				execute:  service.Execute,
				branches: func(plan push.Plan) int { return len(plan.Branches) },
				// The lease rejects a push the remote has moved under, so this
				// changes no outcome — it moves the refusal in front of the
				// network call and names the branch.
				blocked: push.Plan.Blocked,
				noOp:    func(plan push.Plan) bool { return plan.NothingToPublish() },
				notices: flowNotices{
					preview:  "Rerun with --apply to push.",
					noOp:     "The remote already has every selected branch.",
					applied:  "Applied — remote refs updated atomically",
					changed:  "Changes were made.",
					recovery: "The push is atomic, so every selected ref advanced or none did; rerun g2g push to see which.",
				},
			}
			return flow.run(cmd, root, newBudgets(cmd), presentation, apply)
		},
	}
	selection.register(cmd, completions, stack.OfflineSources, "local branch to push (defaults to current branch)", "trunk to use as the push base")
	// A GitHub native stack is linear, so these are the two scopes that can
	// produce one. stack still refuses when it forks, naming the remedy.
	selection.registerScopeOf(cmd, pushCommand, "push")
	cmd.Flags().StringVar(&remote, "remote", localgit.DefaultRemote, "Git remote to push to")
	cmd.Flags().BoolVar(&apply, "apply", false, "atomically push with --force-with-lease after revalidation")
	registerNoSetUpstream(cmd, &noSetUpstream)
	return cmd
}

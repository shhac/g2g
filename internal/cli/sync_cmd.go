package cli

import (
	"context"
	"io"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
	syncer "github.com/shhac/g2g/internal/sync"
)

func newSync(service syncer.Service, guard func(context.Context) error, presentation Presentation) *cobra.Command {
	var selection graphOptions
	var remote string
	var take string
	var apply bool
	cmd := &cobra.Command{
		Use:     "sync",
		GroupID: groupMaintain,
		Short:   "Bring a stack up to date with its remote: fetch, advance the base, replay (preview by default)",
		Args:    cobra.NoArgs,
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		presentation := presentation.resolve(cmd)
		if err := selection.validateScope(); err != nil {
			return err
		}
		chosen, err := syncer.ParseTake(take)
		if err != nil {
			return err
		}
		ctx := commandContext(cmd.Context(), cmd, "sync", applyMode(apply), selection.branch, "")
		flow := applyFlow[syncer.Plan]{
			guard: guard,
			plan: func(ctx context.Context) (syncer.Plan, error) {
				return service.Plan(ctx, selection.Selection(), remote, chosen)
			},
			revalidate: func(ctx context.Context, preview syncer.Plan) (syncer.Plan, error) {
				return service.Revalidate(ctx, selection.Selection(), remote, chosen, preview)
			},
			render:   func(w io.Writer, plan syncer.Plan, p Presentation) error { return writeStackView(w, syncView(plan), p) },
			execute:  service.Apply,
			branches: func(plan syncer.Plan) int { return len(plan.Restack.Steps) + 1 },
			noOp:     func(plan syncer.Plan) bool { return plan.Nothing() },
			blocked:  func(plan syncer.Plan) string { return plan.Blocked },
			// A sync is a sequence, so it can stop between steps. It
			// deliberately does not unwind: the fetch and the fast-forward are
			// wanted regardless, and the replay is resumable through the
			// command that owns it.
			interrupted: func(ctx context.Context, _ error) (bool, error) {
				stopped, err := service.Restack.InProgress(ctx)
				if err != nil || !stopped {
					return false, nil
				}
				return true, stoppedMidSync(cmd, presentation)
			},
			notices: flowNotices{
				preview:       "Rerun with --apply to bring the stack up to date.",
				noOp:          "The stack is already up to date.",
				applied:       "Synced.",
				changed:       "The stack sits on the current base.",
				recovery:      "The base may already have been advanced; rerunning is safe.",
				suggestedNext: "g2g status",
			},
		}
		return flow.run(cmd, ctx, newBudgets(cmd), presentation, apply)
	}
	cmd.Flags().StringVar(&remote, "remote", "origin", "Git remote to read the base from")
	cmd.Flags().BoolVar(&apply, "apply", false, "perform the sequence instead of previewing it")
	// An enum rather than a boolean, because which side wins has more answers
	// than the one implemented and naming the value leaves room for them. There
	// is no "mine": sync moves toward this checkout and push moves toward the
	// remote, so that choice is already made by which command you run.
	cmd.Flags().StringVar(&take, "take", "", "resolve a divergence by taking one side: published (discards local commits the remote does not have)")
	_ = cmd.RegisterFlagCompletionFunc("take", completionCallback(func(context.Context, string) ([]string, error) {
		values := make([]string, 0, len(syncer.Takes))
		for _, value := range syncer.Takes {
			values = append(values, string(value))
		}
		return values, nil
	}))
	selection.registerBranch(cmd, service.Graph)
	// sync was the only mutating stack command with no scope at all, so the
	// boundary it acts on was whatever it hardcoded. Only two values mean
	// anything here: see shape.SyncScopes.
	selection.registerScope(cmd, shape.SyncScopes, stack.ScopeStack, scopeUsage("sync", shape.SyncScopes))
	return cmd
}

// stoppedMidSync reports a sequence that got part-way. It deliberately does
// not unwind: the fetch and the fast-forward are wanted regardless, and the
// replay is resumable through the command that owns it.
func stoppedMidSync(cmd *cobra.Command, p Presentation) error {
	_ = prose(cmd.OutOrStdout(), p, p.problem("The replay stopped part-way."))
	return prose(cmd.OutOrStdout(), p, p.subdued("The base is up to date. Finish with "+runnable("g2g restack --continue")+", or undo the replay with "+runnable("g2g restack --abort")+"."))
}

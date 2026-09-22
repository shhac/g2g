package cli

import (
	"context"
	"errors"
	"io"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/shape"
	syncer "github.com/shhac/g2g/internal/sync"
)

func newSync(service syncer.Service, guard func(context.Context) error, presentation Presentation) *cobra.Command {
	var selection graphOptions
	var remote string
	var take, through string
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
		chosen, err := syncer.ParseTake(take, through)
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
				preview:  "Rerun with --apply to bring the stack up to date.",
				noOp:     "The stack is already up to date.",
				applied:  "Synced.",
				changed:  "The stack sits on the current base.",
				recovery: "The base may already have been advanced; rerunning is safe.",
				// A replay leaves the published branches behind their local
				// ones; push previews what publishing them would do.
				suggestedNext: "g2g push",
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
		values := make([]string, 0, len(syncer.Sides))
		for _, value := range syncer.Sides {
			values = append(values, string(value))
		}
		return values, nil
	}))
	// A boundary on that choice, so it can be made for the part of the stack
	// you rebased elsewhere without being made for the part you did not.
	// Everything above it keeps the default, which is to refuse rather than
	// pick a side silently.
	cmd.Flags().StringVar(&through, "through", "", "with --take, the last branch the chosen side applies to · above it a divergence is still refused")
	_ = cmd.RegisterFlagCompletionFunc("through", completionCallback(localBranchCompletions(service.Graph)))
	selection.registerBranch(cmd, service.Graph)
	// sync was the only mutating stack command with no scope at all, so the
	// boundary it acts on was whatever it hardcoded. Only two values mean
	// anything here: see shape.SyncScopes.
	selection.registerScope(cmd, shape.SyncScopes, shape.ScopeStack, scopeUsage("sync", shape.SyncScopes))
	return cmd
}

// stoppedMidSync reports a sequence that got part-way. It deliberately does
// not unwind: the fetch and the fast-forward are wanted regardless, and the
// replay is resumable through the command that owns it.
// It reports and then marks the run as stopped, so the exit status says what
// the prose says. A replay that stopped on a conflict has left the stack
// part-way through and needs the person back.
func stoppedMidSync(cmd *cobra.Command, p Presentation) error {
	_ = prose(cmd.OutOrStdout(), p, p.problem("The replay stopped part-way."))
	if err := prose(cmd.OutOrStdout(), p, p.subdued("The base is up to date. Finish with "+runnable("g2g restack --continue")+", or undo the replay with "+runnable("g2g restack --abort")+".")); err != nil {
		return err
	}
	return stoppedPartWay(errors.New("the replay stopped part-way"))
}

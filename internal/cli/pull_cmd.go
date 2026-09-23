package cli

import (
	"context"
	"errors"
	"io"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/prune"
	"github.com/shhac/g2g/internal/shape"
	syncer "github.com/shhac/g2g/internal/sync"
)

func newPull(service syncer.Service, pruner prune.Service, guard func(context.Context) error, presentation Presentation) *cobra.Command {
	var selection graphOptions
	var remote string
	var take, through string
	var apply, alsoPrune bool
	cmd := &cobra.Command{
		Use:     "pull",
		GroupID: groupUpdate,
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
		// Machine output is one document, and this writes two reports.
		if alsoPrune && presentation.machine() {
			return errors.New("--prune writes two reports and --json or --porcelain is one document · run g2g pull and then g2g prune")
		}
		ctx := commandContext(cmd.Context(), cmd, applyMode(apply), selection.branch, "")
		pull := pullFlow(cmd, service, selection.Selection(), remote, chosen, guard, presentation, alsoPrune)
		if !alsoPrune {
			return pull.run(cmd, ctx, newBudgets(cmd), presentation, apply)
		}
		return pullThenPrune(cmd, ctx, pull, pruneFlow(pruner, selection.Selection(), guard), presentation, apply)
	}
	cmd.Flags().StringVar(&remote, "remote", "origin", "Git remote to read the base from")
	// Offered only where the build can prune, rather than offered and refused.
	if pruner.Ready() {
		cmd.Flags().BoolVar(&alsoPrune, "prune", false, "then forget the branches whose work has landed, as g2g prune does")
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "perform the sequence instead of previewing it")
	// An enum rather than a boolean, because which side wins has more answers
	// than the one implemented and naming the value leaves room for them. There
	// is no "mine": pull moves toward this checkout and push moves toward the
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
	// pull, as sync, was the only mutating stack command with no scope at all, so the
	// boundary it acts on was whatever it hardcoded. Only two values mean
	// anything here: see shape.SyncScopes.
	selection.registerScope(cmd, shape.SyncScopes, shape.ScopeStack, scopeUsage("pull", shape.SyncScopes))
	return cmd
}

// pullFlow is pull's safety sequence over one selection. thenPrune says a
// prune follows it, which changes what a preview promises and what comes next.
func pullFlow(cmd *cobra.Command, service syncer.Service, selection graph.Selection, remote string, chosen syncer.Take, guard func(context.Context) error, p Presentation, thenPrune bool) applyFlow[syncer.Plan] {
	notices := flowNotices{
		preview:  "Rerun with --apply to bring the stack up to date.",
		noOp:     "The stack is already up to date.",
		applied:  "Pulled.",
		changed:  "The stack sits on the current base.",
		recovery: "The base may already have been advanced; rerunning is safe.",
		// A replay leaves the published branches behind their local ones;
		// push previews what publishing them would do.
		suggestedNext: "g2g push",
	}
	if thenPrune {
		// What has landed is only known once the base has moved, so a
		// preview cannot show the prune it would do; it says it will do one.
		notices.preview = "Rerun with --apply to bring the stack up to date and then forget what has landed."
		notices.suggestedNext = ""
	}
	return applyFlow[syncer.Plan]{
		guard: guard,
		plan: func(ctx context.Context) (syncer.Plan, error) {
			return service.Plan(ctx, selection, remote, chosen)
		},
		revalidate: func(ctx context.Context, preview syncer.Plan) (syncer.Plan, error) {
			return service.Revalidate(ctx, selection, remote, chosen, preview)
		},
		render:   func(w io.Writer, plan syncer.Plan, p Presentation) error { return writeStackView(w, pullView(plan), p) },
		execute:  service.Apply,
		branches: func(plan syncer.Plan) int { return len(plan.Restack.Steps) + 1 },
		noOp:     func(plan syncer.Plan) bool { return plan.Nothing() },
		blocked:  func(plan syncer.Plan) string { return plan.Blocked },
		// A pull is a sequence, so it can stop between steps. It deliberately
		// does not unwind: the fetch and the fast-forward are wanted
		// regardless, and the replay is resumable through the command that
		// owns it.
		interrupted: func(ctx context.Context, cause error) (bool, error) {
			if stopped, err := service.Restack.InProgress(ctx); err == nil && stopped {
				return true, stoppedMidSync(cmd, p)
			}
			var moved *syncer.Stopped
			if !errors.As(cause, &moved) {
				return false, nil
			}
			return true, stoppedAfterMoving(cmd, moved, p)
		},
		notices: notices,
	}
}

// pullThenPrune runs the pull and, once it has happened, the prune over the
// same selection. A preview is the pull's alone.
func pullThenPrune(cmd *cobra.Command, ctx context.Context, pull applyFlow[syncer.Plan], forget applyFlow[prune.Plan], p Presentation, apply bool) error {
	if err := pull.run(cmd, ctx, newBudgets(cmd), p, apply); err != nil || !apply {
		return err
	}
	if err := prose(cmd.OutOrStdout(), p, ""); err != nil {
		return err
	}
	if err := forget.run(cmd, ctx, newBudgets(cmd), p, true); err != nil {
		return stoppedAfterPull(cmd, err, p)
	}
	return nil
}

// stoppedAfterPull reports a prune that did not happen after a pull that did.
// The pull stays happened, so it is a stop part-way rather than a failure to
// retry — and the exit status says only that, so the reason has to be on the
// page, once: a refusal the prune's own flow has already printed is not said
// again, and a failure before it could print anything is.
func stoppedAfterPull(cmd *cobra.Command, cause error, p Presentation) error {
	if !toldNotApplied(cause) {
		if err := prose(cmd.OutOrStdout(), p, p.problem("The prune could not run: "+cause.Error())); err != nil {
			return err
		}
	}
	if err := prose(cmd.OutOrStdout(), p, p.subdued("The pull stands and nothing was forgotten · run "+runnable("g2g prune")+" once that is resolved.")); err != nil {
		return err
	}
	return stoppedPartWay(cause)
}

// stoppedAfterMoving reports a sync that brought some branches down and then
// failed without leaving a replay to resume.
func stoppedAfterMoving(cmd *cobra.Command, stopped *syncer.Stopped, p Presentation) error {
	if err := prose(cmd.OutOrStdout(), p, "\n"+p.problem("Stopped part-way: "+stopped.Err.Error())); err != nil {
		return err
	}
	if err := prose(cmd.OutOrStdout(), p, p.subdued("Brought "+branchList(stopped.Moved)+" to what the remote holds, and "+pick(len(stopped.Moved), "it stays", "they stay")+". Rerun "+runnable("g2g pull")+" to see what is left.")); err != nil {
		return err
	}
	return stoppedPartWay(stopped)
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

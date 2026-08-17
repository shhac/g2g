package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	syncer "github.com/shhac/g2g/internal/sync"
)

func newSync(service syncer.Service, guard func(context.Context) error, presentation Presentation) *cobra.Command {
	var selection graphOptions
	var remote string
	var apply bool
	cmd := &cobra.Command{
		Use:     "sync",
		GroupID: groupMaintain,
		Short:   "Bring a stack up to date with its remote: fetch, advance the base, replay (preview by default)",
		Args:    cobra.NoArgs,
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		presentation := presentation.resolve(cmd)
		ctx := commandContext(cmd.Context(), cmd, "sync", applyMode(apply), selection.branch, "")
		budgets := newBudgets(cmd)

		discoveryCtx, cancel := budgets.discovery(ctx)
		defer cancel()
		// sync replays through restack, and restack's journal holds the only
		// record of where the branches were. Saving a second record over an
		// unfinished one destroys the tips --abort would have restored, so this
		// refuses for the same reason every other mutating command does, at the
		// same point in the sequence applyFlow refuses: before planning, and
		// for the preview too.
		if guard != nil {
			if err := guard(discoveryCtx); err != nil {
				return err
			}
		}
		plan, err := service.Plan(discoveryCtx, selection.Selection(), remote)
		if err != nil {
			return err
		}
		if !apply {
			if err := writeStackView(cmd.OutOrStdout(), syncView(plan), presentation); err != nil {
				return err
			}
			return prose(cmd.OutOrStdout(), presentation, "\n"+presentation.notice("No changes were made.")+" Rerun with --apply to bring the stack up to date.")
		}
		if plan.Blocked != "" {
			return writeNotApplied(cmd.OutOrStdout(), presentation, fmt.Errorf("%s", plan.Blocked))
		}
		return applySync(cmd, ctx, budgets, service, plan, presentation)
	}
	cmd.Flags().StringVar(&remote, "remote", "origin", "Git remote to read the base from")
	cmd.Flags().BoolVar(&apply, "apply", false, "perform the sequence instead of previewing it")
	selection.registerBranch(cmd, service.Graph)
	return cmd
}

// applySync renders and flushes before it changes anything, and reports how
// far it got rather than claiming nothing happened.
func applySync(cmd *cobra.Command, ctx context.Context, budgets budgets, service syncer.Service, plan syncer.Plan, p Presentation) error {
	if err := writeReadyBanner(cmd.OutOrStdout(), p); err != nil {
		return err
	}
	if err := writeStackView(cmd.OutOrStdout(), syncView(plan), p); err != nil {
		return err
	}
	if err := flushOutput(cmd.OutOrStdout()); err != nil {
		return writeNotApplied(cmd.OutOrStdout(), p, err)
	}

	mutateCtx, cancel := budgets.mutation(ctx, len(plan.Restack.Steps)+1)
	defer cancel()
	if err := service.Apply(mutateCtx, plan); err != nil {
		interrupted, checkErr := service.Restack.InProgress(mutateCtx)
		if checkErr == nil && interrupted {
			return stoppedMidSync(cmd, p)
		}
		return writeNotApplied(cmd.OutOrStdout(), p, err)
	}
	_ = prose(cmd.OutOrStdout(), p, p.notice("Synced."))
	return prose(cmd.OutOrStdout(), p, p.subdued("The stack sits on the current base."))
}

// stoppedMidSync reports a sequence that got part-way. It deliberately does
// not unwind: the fetch and the fast-forward are wanted regardless, and the
// replay is resumable through the command that owns it.
func stoppedMidSync(cmd *cobra.Command, p Presentation) error {
	_ = prose(cmd.OutOrStdout(), p, p.problem("The replay stopped part-way."))
	return prose(cmd.OutOrStdout(), p, p.subdued("The base is up to date. Finish with g2g restack --continue, or undo the replay with g2g restack --abort."))
}

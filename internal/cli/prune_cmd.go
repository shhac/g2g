package cli

import (
	"context"
	"errors"
	"io"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/prune"
	"github.com/shhac/g2g/internal/shape"
)

func newPrune(service prune.Service, guard func(context.Context) error, presentation Presentation) *cobra.Command {
	var selection graphOptions
	var apply bool
	var cleanup prune.Options
	cmd := &cobra.Command{
		Use:     "prune",
		GroupID: groupUpdate,
		Short:   "Forget landed branches; local deletion is opt-in (preview by default)",
		Args:    cobra.NoArgs,
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		presentation := presentation.resolve(cmd)
		if err := selection.validateScope(); err != nil {
			return err
		}
		ctx := commandContext(cmd.Context(), cmd, applyMode(apply), selection.branch, "")
		return pruneFlow(service, selection.Selection(), guard, cmd, presentation, cleanup).run(cmd, ctx, newBudgets(cmd), presentation, apply)
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "perform the listed cleanup instead of previewing the change")
	cmd.Flags().BoolVar(&cleanup.DeleteBranches, "delete-branches", false, "also delete the local branches whose work is already upstream (refuses checked-out branches)")
	cmd.Flags().BoolVar(&cleanup.ForgetMissing, "forget-missing", false, "also forget records for missing local branches, without stranding surviving children")
	selection.registerBranch(cmd, service.Graph)
	// Pruning never rewrites, so it can range as wide as a read. It defaults to
	// the stack being worked on, which is the boundary
	// sync uses, because "what has landed" is asked about a stack rather than
	// about a repository.
	selection.registerScope(cmd, shape.ReadScopes, graph.ScopeStack, scopeUsage("forget", shape.ReadScopes))
	return cmd
}

// pruneFlow is prune's safety sequence over one selection, which pull --prune
// runs too once the base has moved.
func pruneFlow(service prune.Service, selection graph.Selection, guard func(context.Context) error, cmd *cobra.Command, p Presentation, cleanup prune.Options) applyFlow[prune.Plan] {
	notices := flowNotices{
		preview: "Rerun with --apply to forget them.", noOp: "Nothing has landed.", applied: "Forgotten.",
		changed: "The graph no longer records them. No branch was deleted.", suggestedNext: "g2g status",
	}
	if cleanup.ForgetMissing {
		notices.noOp = "No landed branches or missing records to clean up."
	}
	if cleanup.DeleteBranches {
		notices.noOp = "No eligible local branches or records to clean up."
		notices.preview = "Rerun with --apply to forget the records and delete the listed local branches."
		notices.changed = "The listed cleanup is complete. Remote branches were untouched."
	}
	return applyFlow[prune.Plan]{
		guard: guard,
		plan:  func(ctx context.Context) (prune.Plan, error) { return service.PlanWithOptions(ctx, selection, cleanup) },
		revalidate: func(ctx context.Context, preview prune.Plan) (prune.Plan, error) {
			return service.Revalidate(ctx, selection, preview)
		},
		render:   func(w io.Writer, plan prune.Plan, p Presentation) error { return writePrunePlan(w, plan, p) },
		execute:  func(ctx context.Context, plan prune.Plan) error { return service.Apply(ctx, plan) },
		branches: func(plan prune.Plan) int { return len(plan.Forgotten()) },
		noOp:     func(plan prune.Plan) bool { return plan.Nothing() },
		// Forgetting a branch while something recorded under it survives is
		// a refusal, so it belongs before the ready banner rather than
		// after it, in Apply.
		blocked: func(plan prune.Plan) string { return plan.Blocked },
		interrupted: func(_ context.Context, cause error) (bool, error) {
			var stopped *prune.Stopped
			if !errors.As(cause, &stopped) {
				return false, nil
			}
			return true, writeStoppedPartWay(cmd.OutOrStdout(), p, "Cleanup stopped part-way: "+stopped.Err.Error(), stopped.WhatStands()+" Preview "+runnable(stopped.Retry)+" to see what remains.", stopped)
		},
		notices: notices,
	}
}

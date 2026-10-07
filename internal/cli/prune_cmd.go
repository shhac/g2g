package cli

import (
	"context"
	"errors"
	"io"
	"slices"

	"github.com/spf13/cobra"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/prune"
	"github.com/shhac/g2g/internal/push"
)

func newPrune(service prune.Service, published push.Known, guard func(context.Context) error, presentation Presentation) *cobra.Command {
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
		return pruneFlow(service, published, localgit.DefaultRemote, selection.Selection(), guard, cmd, presentation, cleanup).run(cmd, ctx, newBudgets(cmd), presentation, apply)
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "perform the listed cleanup instead of previewing the change")
	cmd.Flags().BoolVar(&cleanup.DeleteBranches, "delete-branches", false, "also delete the local branches whose work is already upstream (refuses checked-out branches)")
	cmd.Flags().BoolVar(&cleanup.ForgetMissing, "forget-missing", false, "also forget records for missing local branches, without stranding surviving children")
	selection.registerBranch(cmd, service.Graph)
	// Pruning never rewrites, so it can range as wide as a read. It defaults to
	// the stack being worked on, which is the boundary
	// sync uses, because "what has landed" is asked about a stack rather than
	// about a repository.
	selection.registerScopeOf(cmd, pruneCommand, "forget")
	return cmd
}

// pruneFlow is prune's safety sequence over one selection, which pull --prune
// runs too once the base has moved.
func pruneFlow(service prune.Service, published push.Known, remote string, selection graph.Selection, guard func(context.Context) error, cmd *cobra.Command, p Presentation, cleanup prune.Options) applyFlow[prunePlan] {
	notices := flowNotices{
		preview: "Rerun with --apply to forget them.", noOp: "Nothing has landed.", applied: "Forgotten.",
		changed: "The graph no longer records them. No branch was deleted.",
	}
	if cleanup.ForgetMissing {
		notices.noOp = "No landed branches or missing records to clean up."
	}
	if cleanup.DeleteBranches {
		notices.noOp = "No eligible local branches or records to clean up."
		notices.preview = "Rerun with --apply to forget the records and delete the listed local branches."
		notices.changed = "The listed cleanup is complete. Remote branches were untouched."
	}
	return applyFlow[prunePlan]{
		guard: guard,
		plan: func(ctx context.Context) (prunePlan, error) {
			plan, err := service.PlanWithOptions(ctx, selection, cleanup)
			return prunePlan{Plan: plan, remote: remote}, err
		},
		// Only the plan an apply carries out is asked how what it keeps is
		// published, since only it is followed by a suggestion.
		revalidate: func(ctx context.Context, preview prunePlan) (prunePlan, error) {
			plan, err := service.Revalidate(ctx, selection, preview.Plan)
			if err != nil {
				return prunePlan{}, err
			}
			return prunePlan{Plan: plan, remote: remote, unpublished: keepsUnpublished(ctx, published, remote, plan)}, nil
		},
		render:   func(w io.Writer, plan prunePlan, p Presentation) error { return writePrunePlan(w, plan.Plan, p) },
		execute:  func(ctx context.Context, plan prunePlan) error { return service.Apply(ctx, plan.Plan) },
		branches: func(plan prunePlan) int { return len(plan.Forgotten()) },
		noOp:     func(plan prunePlan) bool { return plan.Nothing() },
		// Forgetting a branch while something recorded under it survives is
		// a refusal, so it belongs before the ready banner rather than
		// after it, in Apply.
		blocked: func(plan prunePlan) string { return plan.Blocked },
		suggest: pruneNext,
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

// prunePlan is a prune with how the branches it keeps stand against the
// remote, which is all its suggestion needs. After a pull those branches have
// been replayed and are not published as they are here, so push follows;
// status sends the reader there only one run later.
type prunePlan struct {
	prune.Plan
	remote      string
	unpublished bool
}

// keepsUnpublished reports whether a branch the prune keeps is not on the
// remote as it is here, by the comparison status makes from local refs alone.
// It is asked for a suggestion, so a failure to answer is no answer rather
// than an error, and a trunk is published by landing on it, never by pushing.
func keepsUnpublished(ctx context.Context, published push.Known, remote string, plan prune.Plan) bool {
	kept := plan.Discovery
	forgotten := plan.Forgotten()
	kept.Branches = slices.DeleteFunc(slices.Clone(kept.Branches), func(branch string) bool {
		_, tracked := kept.Graph.Parent(branch)
		return !tracked || slices.Contains(forgotten, branch)
	})
	publishing, err := readPublished(ctx, published, remote, false, kept)
	if err != nil {
		return false
	}
	for _, publication := range publishing {
		switch publication.Standing {
		case push.New, push.Rewritten, push.Ahead:
			return true
		}
	}
	return false
}

// pruneNext is push when the prune kept something to publish, aimed at what
// it selected, and status over the same selection otherwise.
func pruneNext(plan prunePlan) string {
	if !plan.unpublished {
		return statusNext(plan.Discovery)
	}
	target := pushCommand
	if plan.remote != localgit.DefaultRemote {
		target.command += " --remote " + plan.remote
	}
	return selectedIn(plan.Discovery).next(target)
}

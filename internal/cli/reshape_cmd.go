package cli

import (
	"context"
	"errors"
	"io"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/reshape"
)

// newReshape builds the commands that change which branches a stack is made
// of. They take the graph service only to complete --branch from the same
// local branches track offers; everything they do goes through reshape.
func newReshape(service reshape.Service, branches graph.Service, guard func(context.Context) error, presentation Presentation) []*cobra.Command {
	return []*cobra.Command{
		newRemoval(reshape.Delete, service, branches, guard, presentation),
		newRemoval(reshape.Fold, service, branches, guard, presentation),
		newRename(service, branches, guard, presentation),
	}
}

// removal is what differs between delete and fold: the words.
type removal struct {
	short, long, applyUsage string
	notices                 flowNotices
}

var removals = map[reshape.Operation]removal{
	reshape.Delete: {
		short: "Delete a branch and record what sat on it on its parent (preview by default)",
		long: "Deletes the local branch and records each branch that sat on it on the branch it sat on, " +
			"which is what asking for it to go means. Each keeps its fork point, so the next restack replays " +
			"only its own commits onto the new parent and the deleted branch's commits leave it; the preview " +
			"names any of them that exist nowhere else. The remote branch is not touched.",
		applyUsage: "delete the branch and record its children on its parent instead of previewing",
		notices: flowNotices{
			preview:  "Rerun with --apply to delete it.",
			applied:  "Deleted.",
			changed:  "The branch is gone and what sat on it is recorded on its parent.",
			recovery: "The branch may already be gone · run g2g graph to see what is recorded.",
		},
	},
	reshape.Fold: {
		short: "Fold a branch's commits into its parent and remove it (preview by default)",
		long: "Fast-forwards the parent to the branch, so its commits become the parent's, then deletes the " +
			"branch and records what sat on it on the parent. Only a parent the branch sits directly on can " +
			"be fast-forwarded; one that has moved on needs a restack first. A trunk is never moved: a branch " +
			"joins its trunk through its pull request.",
		applyUsage: "fast-forward the parent and remove the branch instead of previewing",
		notices: flowNotices{
			preview:  "Rerun with --apply to fold it.",
			applied:  "Folded.",
			changed:  "The parent holds the branch's commits, the branch is gone, and what sat on it is recorded on the parent.",
			recovery: "The parent may already have moved · run g2g graph to see what is recorded.",
		},
	},
}

func newRemoval(operation reshape.Operation, service reshape.Service, branches graph.Service, guard func(context.Context) error, presentation Presentation) *cobra.Command {
	words := removals[operation]
	var branch string
	var apply bool
	cmd := &cobra.Command{
		Use:     string(operation),
		GroupID: groupStructure,
		Short:   words.short,
		Long:    words.long,
		Args:    cobra.NoArgs,
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		presentation := presentation.resolve(cmd)
		root := commandContext(cmd.Context(), cmd, string(operation), applyMode(apply), branch, "")
		flow := applyFlow[reshape.Plan]{
			plan: func(ctx context.Context) (reshape.Plan, error) {
				return service.Plan(ctx, operation, branch)
			},
			revalidate: func(ctx context.Context, preview reshape.Plan) (reshape.Plan, error) {
				return service.Revalidate(ctx, operation, branch, preview)
			},
			render:      writeRemovalPlan,
			guard:       guard,
			execute:     service.Apply,
			branches:    func(plan reshape.Plan) int { return 1 + len(plan.Children) },
			blocked:     func(plan reshape.Plan) string { return plan.Blocked },
			interrupted: reshapeInterrupted(cmd.OutOrStdout(), presentation),
			suggest:     removalNext,
			notices:     words.notices,
		}
		return flow.run(cmd, root, newBudgets(cmd), presentation, apply)
	}
	cmd.Flags().StringVar(&branch, "branch", "", "branch to "+string(operation)+" (defaults to the current branch)")
	_ = cmd.RegisterFlagCompletionFunc("branch", completionCallback(localBranchCompletions(branches)))
	cmd.Flags().BoolVar(&apply, "apply", false, words.applyUsage)
	return cmd
}

func newRename(service reshape.Service, branches graph.Service, guard func(context.Context) error, presentation Presentation) *cobra.Command {
	var branch string
	var apply bool
	cmd := &cobra.Command{
		Use:     "rename <new-name>",
		GroupID: groupStructure,
		Short:   "Rename a branch and every record of it (preview by default)",
		Long: "Renames the local branch with git branch -m and rewrites the g2g graph to match: its own edge, " +
			"the branches recorded on it, its place among the trunks, and its fork-point ref. A branch already " +
			"published stays published under its old name, and so does its pull request.",
		Args: cobra.ExactArgs(1),
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		presentation := presentation.resolve(cmd)
		name := args[0]
		root := commandContext(cmd.Context(), cmd, "rename", applyMode(apply), branch, "")
		flow := applyFlow[reshape.RenamePlan]{
			plan: func(ctx context.Context) (reshape.RenamePlan, error) {
				return service.PlanRename(ctx, branch, name)
			},
			revalidate: func(ctx context.Context, preview reshape.RenamePlan) (reshape.RenamePlan, error) {
				return service.RevalidateRename(ctx, branch, name, preview)
			},
			render:      writeRenamePlan,
			guard:       guard,
			execute:     service.ApplyRename,
			branches:    func(plan reshape.RenamePlan) int { return 1 + len(plan.Children) },
			blocked:     func(plan reshape.RenamePlan) string { return plan.Blocked },
			interrupted: reshapeInterrupted(cmd.OutOrStdout(), presentation),
			notices: flowNotices{
				preview:       "Rerun with --apply to rename it.",
				applied:       "Renamed.",
				changed:       "The branch and every record of it carry the new name.",
				recovery:      "The branch may already be renamed · run g2g graph to see what is recorded.",
				suggestedNext: "g2g graph",
			},
		}
		return flow.run(cmd, root, newBudgets(cmd), presentation, apply)
	}
	cmd.Flags().StringVar(&branch, "branch", "", "branch to rename (defaults to the current branch)")
	_ = cmd.RegisterFlagCompletionFunc("branch", completionCallback(localBranchCompletions(branches)))
	cmd.Flags().BoolVar(&apply, "apply", false, "rename the branch and its records instead of previewing")
	return cmd
}

// removalNext is the command that finishes what a removal starts: the
// branches it moved, or the ones it left behind, replayed onto where they now
// sit. More than one on a trunk has no single command that reaches them all
// and nothing else, so the graph is where to look.
func removalNext(plan reshape.Plan) string {
	stale := plan.Children
	if plan.Operation == reshape.Fold {
		// A fold's children already sit on the parent's new tip; its siblings
		// do not.
		stale = plan.Siblings
	}
	switch {
	case len(stale) == 1:
		return "g2g restack --branch " + stale[0]
	case len(stale) > 1 && plan.Discovery.Graph.Tracked(plan.Parent):
		return "g2g restack --branch " + plan.Parent
	case len(stale) > 1:
		return "g2g graph"
	}
	return ""
}

// reshapeInterrupted claims the two failures that are not "nothing happened":
// a removal or rename that finished and could not tidy up, and a rollback that
// could not finish. Both leave something done that is not coming back, which
// is the part-way status.
func reshapeInterrupted(writer io.Writer, p Presentation) func(context.Context, error) (bool, error) {
	return func(_ context.Context, err error) (bool, error) {
		var partial *reshape.Partial
		if errors.As(err, &partial) {
			if err := prose(writer, p, "\n"+p.problem("Stopped part-way: "+partial.Left+": "+partial.Err.Error())); err != nil {
				return true, err
			}
			if err := prose(writer, p, p.subdued(partial.Done+" · run "+runnable("g2g graph")+" to see what is recorded.")); err != nil {
				return true, err
			}
			return true, stoppedPartWay(partial)
		}
		var rolledBack *reshape.RolledBack
		if errors.As(err, &rolledBack) && rolledBack.Stuck() {
			if err := prose(writer, p, "\n"+p.problem("Stopped part-way: "+rolledBack.Error())); err != nil {
				return true, err
			}
			if err := prose(writer, p, p.subdued("Check "+runnable("git status")+" and "+runnable("g2g graph")+" before going on.")); err != nil {
				return true, err
			}
			return true, stoppedPartWay(rolledBack)
		}
		return false, nil
	}
}

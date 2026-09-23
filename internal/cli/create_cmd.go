package cli

import (
	"context"
	"errors"
	"io"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/create"
	"github.com/shhac/g2g/internal/graph"
)

// newCreate takes the graph service only to complete --parent from the same
// local branches track offers; everything it does goes through create.
func newCreate(service create.Service, branches graph.Service, guard func(context.Context) error, presentation Presentation) *cobra.Command {
	var parent, message string
	var apply bool
	cmd := &cobra.Command{
		Use:     "create <branch>",
		GroupID: groupShape,
		Short:   "Start a branch on top of this one and record it (preview by default)",
		Long: "Creates a branch at the tip of its parent, switches to it, and records it under that parent, " +
			"in one step. The parent is the branch you are on unless --parent names another; either way it is " +
			"stated rather than inferred, which is why this needs no candidate list.\n\n" +
			"The parent must already be in the g2g graph, or be the repository's default branch: recording a " +
			"child under a branch the graph does not know would make that branch a trunk. " +
			"-m commits what is staged onto the new branch.",
		Args: cobra.ExactArgs(1),
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		presentation := presentation.resolve(cmd)
		request := create.Request{Name: args[0], Parent: parent, Commit: cmd.Flags().Changed("message"), Message: message}
		root := commandContext(cmd.Context(), cmd, "create", applyMode(apply), "", "")
		flow := applyFlow[create.Plan]{
			plan: func(ctx context.Context) (create.Plan, error) {
				return service.Plan(ctx, request)
			},
			revalidate: func(ctx context.Context, preview create.Plan) (create.Plan, error) {
				return service.Revalidate(ctx, request, preview)
			},
			render:   writeCreatePlan,
			guard:    guard,
			execute:  service.Apply,
			branches: func(create.Plan) int { return 1 },
			blocked:  func(plan create.Plan) string { return plan.Blocked },
			// A commit that fails after the branch is recorded has done most of
			// what was asked, and the branch and its record stay. Reporting that
			// as "not applied" would be wrong about both.
			interrupted: func(_ context.Context, err error) (bool, error) {
				var partial *create.Partial
				if !errors.As(err, &partial) {
					return false, nil
				}
				return true, stoppedMidCreate(cmd.OutOrStdout(), partial, presentation)
			},
			notices: flowNotices{
				preview:       "Rerun with --apply to create it.",
				applied:       "Created.",
				changed:       "The new branch is checked out and recorded in the g2g-owned graph.",
				recovery:      "The branch may already exist and be checked out · run g2g status to see whether it was recorded.",
				suggestedNext: "g2g status",
			},
		}
		return flow.run(cmd, root, newBudgets(cmd), presentation, apply)
	}
	cmd.Flags().StringVar(&parent, "parent", "", "branch to start from and record as the parent (defaults to the current branch)")
	_ = cmd.RegisterFlagCompletionFunc("parent", completionCallback(localBranchCompletions(branches)))
	cmd.Flags().StringVarP(&message, "message", "m", "", "commit what is staged onto the new branch, with this message")
	cmd.Flags().BoolVar(&apply, "apply", false, "create, record and switch instead of previewing")
	return cmd
}

// stoppedMidCreate reports a branch that was created and recorded and then
// could not be committed to. What it says is exactly what is true: the branch
// exists, it is checked out, it is recorded, and the changes are still staged.
func stoppedMidCreate(writer io.Writer, partial *create.Partial, p Presentation) error {
	if err := prose(writer, p, "\n"+p.problem("Stopped part-way: the commit failed: "+partial.Err.Error())); err != nil {
		return err
	}
	if err := prose(writer, p, p.subdued(partial.Branch+" is created, checked out and recorded under "+partial.Parent+", and what was staged is still staged · commit it with "+runnable("git commit")+".")); err != nil {
		return err
	}
	return stoppedPartWay(partial)
}

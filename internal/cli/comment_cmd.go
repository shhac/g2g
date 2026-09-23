package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/comment"
	"github.com/shhac/g2g/internal/stack"
)

func newComment(service comment.Service, completions stack.Completions, guard func(context.Context) error, presentation Presentation) *cobra.Command {
	var selection stackOptions
	var apply bool
	cmd := &cobra.Command{
		Use:     "comment",
		GroupID: groupTools,
		Short:   "Keep a comment on each pull request listing its stack (preview by default)",
		Long: "Keeps one comment on every pull request in the stack, listing the stack from where that pull request " +
			"stands: the trunk, the branches below it, the pull request itself in bold, and everything built on it.\n\n" +
			"A later run edits the comment it finds rather than adding another, so the list follows the stack. Pull " +
			"requests that have merged out of the stack stay listed, because the comments remember them after the " +
			"branch is gone.\n\n" +
			"It keeps the whole stack the branch belongs to, whichever branch it is run from, because each comment " +
			"lists its own pull request's ancestors and descendants and keeping only part of a stack would leave the " +
			"rest describing a different one. Run from a trunk, it keeps every stack on it.",
		Args: cobra.NoArgs,
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		presentation := presentation.resolve(cmd)
		if err := selection.validate(); err != nil {
			return err
		}
		root := commandContext(cmd.Context(), cmd, applyMode(apply), selection.branch, selection.trunk)
		flow := applyFlow[comment.Plan]{
			plan: func(ctx context.Context) (comment.Plan, error) {
				return service.Plan(ctx, selection.Selection())
			},
			revalidate: func(ctx context.Context, preview comment.Plan) (comment.Plan, error) {
				return service.Revalidate(ctx, selection.Selection(), preview)
			},
			render: func(writer io.Writer, plan comment.Plan, p Presentation) error {
				return writeStackView(writer, commentView(plan), p)
			},
			guard:    guard,
			execute:  service.Execute,
			branches: comment.Plan.Changing,
			noOp:     comment.Plan.NothingToDo,
			blocked:  func(plan comment.Plan) string { return plan.Blocked },
			// Comments already written stay written, so a run that fails on
			// the third is not "not applied".
			interrupted: func(_ context.Context, err error) (bool, error) {
				var stopped *comment.Stopped
				if !errors.As(err, &stopped) {
					return false, nil
				}
				return true, stoppedMidComment(cmd, stopped, presentation)
			},
			notices: flowNotices{
				preview:  "Rerun with --apply to write these comments.",
				noOp:     "Every stack comment already says what the stack is. Nothing to do.",
				applied:  "Commented.",
				changed:  "Each pull request now lists its stack.",
				recovery: "Some comments may already be written · rerun g2g github comment --apply to finish, which edits them rather than adding more.",
			},
		}
		return flow.run(cmd, root, newBudgets(cmd), presentation, apply)
	}
	selection.register(cmd, completions, stack.ReadableSources, "a branch of the stack to comment on (defaults to current branch)", "trunk to use as the base")
	cmd.Flags().BoolVar(&apply, "apply", false, "write the comments instead of previewing them")
	return cmd
}

// stoppedMidComment says which comments were written before the run failed.
func stoppedMidComment(cmd *cobra.Command, stopped *comment.Stopped, p Presentation) error {
	writer := cmd.OutOrStdout()
	if err := prose(writer, p, "\n"+p.problem(fmt.Sprintf("Stopped part-way at #%d: %s", stopped.Failed, stopped.Err))); err != nil {
		return err
	}
	written := "Wrote the comment on " + pullRequestList(stopped.Written) + ", and " + pick(len(stopped.Written), "it stays", "they stay") + "."
	if err := prose(writer, p, p.subdued(written+" Rerun "+runnable("g2g github comment --apply")+" to finish; it edits rather than adds.")); err != nil {
		return err
	}
	return stoppedPartWay(stopped)
}

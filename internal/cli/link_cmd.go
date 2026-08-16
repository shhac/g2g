package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shhac/gt2gh/internal/githubstack"
	"github.com/shhac/gt2gh/internal/link"
	"github.com/shhac/gt2gh/internal/stack"
)

func newLink(service link.Service, completions stack.Completions, guard func(context.Context) error, presentation Presentation) *cobra.Command {
	var selection stackOptions
	var apply bool
	cmd := &cobra.Command{
		Use:     "link",
		GroupID: groupPublish,
		Short:   "Link a stack to GitHub's native stacks (preview by default)",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			presentation := presentation.resolve(cmd)
			mode := "preview"
			if apply {
				mode = "apply"
			}
			root := commandContext(cmd.Context(), cmd, "link", mode, selection.branch, selection.trunk)
			flow := applyFlow[link.Plan]{
				plan: func(ctx context.Context) (link.Plan, error) { return service.Plan(ctx, selection.Selection()) },
				revalidate: func(ctx context.Context, preview link.Plan) (link.Plan, error) {
					return service.Revalidate(ctx, selection.Selection(), preview)
				},
				render:   writeLinkPlan,
				guard:    guard,
				execute:  service.Execute,
				branches: func(plan link.Plan) int { return len(plan.Branches) },
				noOp:     link.Plan.NothingToLink,
				notices: flowNotices{
					preview:  "Re-run with --apply to link.",
					noOp:     "No changes were needed or made.",
					applied:  "Applied — GitHub stack updated",
					changed:  "Changes were made.",
					recovery: "Run g2g status to see whether GitHub recorded the link.",
				},
			}
			return flow.run(cmd, root, newBudgets(cmd), presentation, apply)
		},
	}
	selection.register(cmd, completions, "local branch to link (defaults to current branch)", "trunk to use as the link base")
	cmd.Flags().BoolVar(&apply, "apply", false, "invoke gh stack link after revalidation")
	return cmd
}

func writeLinkPlan(writer io.Writer, plan link.Plan, presentation Presentation) error {
	return writeStackView(writer, linkView(plan), presentation)
}

// writeNotApplied renders the outcome of a failed mutation and returns the
// error marked as presented, so the top-level printer reports it without
// repeating the diagnostic block.
func writeNotApplied(writer io.Writer, presentation Presentation, err error) error {
	if presentation.machine() {
		return err
	}
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, presentation.problem("Not applied"))

	summary := err.Error()
	var commandErr *githubstack.CommandError
	if errors.As(err, &commandErr) {
		summary = commandErr.Summary()
	}
	fmt.Fprintln(writer, summary)

	diagnostic := commandDiagnostic(err)
	if diagnostic == "" {
		return err
	}
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, presentation.subdued("Diagnostic:"))
	for _, line := range strings.Split(diagnostic, "\n") {
		fmt.Fprintln(writer, presentation.subdued("  "+line))
	}
	return presentedError{err: err}
}

type outputFlusher interface{ Flush() error }

func flushOutput(writer io.Writer) error {
	if flusher, ok := writer.(outputFlusher); ok {
		if err := flusher.Flush(); err != nil {
			return fmt.Errorf("flush ready-to-apply output: %w", err)
		}
	}
	return nil
}

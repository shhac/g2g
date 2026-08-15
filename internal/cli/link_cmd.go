package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shhac/gt2gh/internal/githubstack"
	"github.com/shhac/gt2gh/internal/link"
	"github.com/shhac/gt2gh/internal/stack"
)

func newLink(service link.Service, completions stack.Completions, presentation Presentation) *cobra.Command {
	var selection stackOptions
	var apply bool
	cmd := &cobra.Command{
		Use:   "link",
		Short: "Link a linear Graphite stack to GitHub (preview by default)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			presentation := presentation.resolve(cmd)
			mode := "preview"
			if apply {
				mode = "apply"
			}
			budgets := newBudgets(cmd)
			root := commandContext(cmd.Context(), cmd, "link", mode, selection.branch, selection.trunk)
			ctx, cancel := budgets.discovery(root)
			defer cancel()
			plan, err := service.Plan(ctx, selection.Selection())
			if err != nil {
				return err
			}
			if !apply {
				if err := writeLinkPlan(cmd.OutOrStdout(), plan, presentation); err != nil {
					return err
				}
				if plan.NothingToLink() {
					prose(cmd.OutOrStdout(), presentation, "\n"+presentation.notice("No changes were needed or made."))
					return nil
				}
				prose(cmd.OutOrStdout(), presentation, "\n"+presentation.notice("No changes were made.")+" Re-run with --apply to link.")
				return nil
			}
			validated, err := service.Revalidate(ctx, selection.Selection(), plan)
			if err != nil {
				return writeNotApplied(cmd.OutOrStdout(), presentation, err)
			}
			if validated.NothingToLink() {
				if err := writeLinkPlan(cmd.OutOrStdout(), validated, presentation); err != nil {
					return err
				}
				err := prose(cmd.OutOrStdout(), presentation, "\n"+presentation.notice("No changes were needed or made."))
				return err
			}
			if err := writeReadyToApply(cmd.OutOrStdout(), validated, presentation); err != nil {
				return fmt.Errorf("render ready-to-apply output: %w", writeNotApplied(cmd.OutOrStdout(), presentation, err))
			}
			if err := flushOutput(cmd.OutOrStdout()); err != nil {
				return writeNotApplied(cmd.OutOrStdout(), presentation, err)
			}
			mutateCtx, cancelMutation := budgets.mutation(root, len(validated.Branches))
			defer cancelMutation()
			if err := service.Execute(mutateCtx, validated); err != nil {
				return writeNotApplied(cmd.OutOrStdout(), presentation, mutationTimeout(err, "Run g2g status to see whether GitHub recorded the link."))
			}
			prose(cmd.OutOrStdout(), presentation, presentation.notice("Applied — GitHub stack updated"))
			prose(cmd.OutOrStdout(), presentation, presentation.subdued("Changes were made."))
			return nil
		},
	}
	selection.register(cmd, completions, "Graphite-tracked local branch to link (defaults to current branch)", "Graphite-declared trunk to use as the link base")
	cmd.Flags().BoolVar(&apply, "apply", false, "invoke gh stack link after revalidation")
	return cmd
}

func writeReadyToApply(writer io.Writer, plan link.Plan, presentation Presentation) error {
	if err := writeReadyBanner(writer, presentation); err != nil {
		return err
	}
	return writeLinkPlan(writer, plan, presentation)
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

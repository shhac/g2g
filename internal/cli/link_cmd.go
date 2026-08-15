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
)

func newLink(service link.Service, presentation Presentation) *cobra.Command {
	var selection stackOptions
	var apply bool
	cmd := &cobra.Command{
		Use:   "link",
		Short: "Link a linear Graphite stack to GitHub (preview by default)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), linkTimeout)
			defer cancel()
			mode := "preview"
			if apply {
				mode = "apply"
			}
			ctx = commandContext(cmd, "link", mode, selection.branch, selection.trunk)
			plan, err := service.PlanWithOptions(ctx, selection.Selection())
			if err != nil {
				return err
			}
			if !apply {
				if err := writeLinkPlan(cmd.OutOrStdout(), plan, presentation); err != nil {
					return err
				}
				if plan.NothingToLink() {
					fmt.Fprintln(cmd.OutOrStdout(), presentation.notice("No changes were needed or made."))
					return nil
				}
				fmt.Fprintln(cmd.OutOrStdout(), presentation.notice("No changes were made.")+" --apply re-discovers and revalidates before invoking gh stack link; copying the displayed command is your deliberate snapshot choice.")
				return nil
			}
			validated, err := service.RevalidateWithOptions(ctx, selection.Selection(), plan)
			if err != nil {
				return writeNotApplied(cmd.OutOrStdout(), presentation, err)
			}
			if validated.NothingToLink() {
				if err := writeLinkPlan(cmd.OutOrStdout(), validated, presentation); err != nil {
					return err
				}
				_, err := fmt.Fprintln(cmd.OutOrStdout(), presentation.notice("No changes were needed or made."))
				return err
			}
			if err := writeReadyToApply(cmd.OutOrStdout(), validated, presentation); err != nil {
				return fmt.Errorf("render ready-to-apply output: %w", writeNotApplied(cmd.OutOrStdout(), presentation, err))
			}
			if err := flushOutput(cmd.OutOrStdout()); err != nil {
				return writeNotApplied(cmd.OutOrStdout(), presentation, err)
			}
			if err := service.Execute(ctx, validated); err != nil {
				return writeNotApplied(cmd.OutOrStdout(), presentation, err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), presentation.notice("Applied — GitHub stack updated"))
			fmt.Fprintln(cmd.OutOrStdout(), presentation.subdued("Changes were made."))
			return nil
		},
	}
	selection.register(cmd, service, "Graphite-tracked local branch to link (defaults to current branch)", "Graphite-declared trunk to use as the link base")
	cmd.Flags().BoolVar(&apply, "apply", false, "invoke gh stack link after revalidation")
	return cmd
}

func writeReadyToApply(writer io.Writer, plan link.Plan, presentation Presentation) error {
	if _, err := fmt.Fprintln(writer, presentation.accent("Ready to apply")); err != nil {
		return err
	}
	return writeLinkPlan(writer, plan, presentation)
}

func writeLinkPlan(writer io.Writer, plan link.Plan, presentation Presentation) error {
	preview := newLinkPreview(plan)
	if _, err := fmt.Fprintf(writer, "%s: %s\n", presentation.accent("Target"), presentation.branch(preview.Target)); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(writer); err != nil {
		return err
	}
	for index, node := range preview.Nodes {
		if node.Trunk {
			if _, err := fmt.Fprintf(writer, "  %s\n", presentation.trunk(node.Branch+" (trunk)")); err != nil {
				return err
			}
			continue
		}
		label := presentation.branch(node.Branch) + " (" + presentation.pr(fmt.Sprintf("#%d", node.PRNumber)) + ")"
		if node.Unresolved != "" {
			label = presentation.branch(node.Branch) + " " + presentation.problem(fmt.Sprintf("(unresolved: %s)", node.Unresolved))
		}
		if _, err := fmt.Fprintf(writer, "%s└─ %s\n", strings.Repeat("  ", index), label); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(writer); err != nil {
		return err
	}
	if preview.NothingToLink {
		if _, err := fmt.Fprintln(writer, "Nothing to link — this stack has one pull request."); err != nil {
			return err
		}
	} else {
		if err := writeCommand(writer, preview.commandText(), presentation); err != nil {
			return err
		}
	}
	if preview.ApplyBlocked {
		if _, err := fmt.Fprintln(writer, presentation.problem("Apply blocked: resolve every unresolved GitHub PR mapping first.")); err != nil {
			return err
		}
	}
	return nil
}

func writeCommand(writer io.Writer, command string, presentation Presentation) error {
	if _, err := fmt.Fprintln(writer, presentation.accent("Command to run")); err != nil {
		return err
	}
	_, err := fmt.Fprintln(writer, presentation.command(command))
	return err
}

// writeNotApplied renders the outcome of a failed mutation and returns the
// error marked as presented, so the top-level printer reports it without
// repeating the diagnostic block.
func writeNotApplied(writer io.Writer, presentation Presentation, err error) error {
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

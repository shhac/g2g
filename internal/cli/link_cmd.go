package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shhac/gt2gh/internal/link"
)

func newLink(service link.Service, presentation Presentation) *cobra.Command {
	var branch string
	var trunk string
	var apply bool
	cmd := &cobra.Command{
		Use:   "link",
		Short: "Link a linear Graphite stack to GitHub (preview by default)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), linkTimeout)
			defer cancel()
			plan, err := service.PlanWithTrunk(ctx, branch, trunk)
			if err != nil {
				return err
			}
			if !apply {
				if err := writeLinkPlan(cmd.OutOrStdout(), plan, presentation); err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), presentation.notice("No changes were made.")+" --apply re-discovers and revalidates before invoking gh stack link; copying the displayed command is your deliberate snapshot choice.")
				return nil
			}
			validated, err := service.RevalidateWithTrunk(ctx, branch, trunk, plan)
			if err != nil {
				writeNotApplied(cmd.OutOrStdout(), presentation, err)
				return err
			}
			if err := writeReadyToApply(cmd.OutOrStdout(), validated, presentation); err != nil {
				writeNotApplied(cmd.OutOrStdout(), presentation, err)
				return fmt.Errorf("render ready-to-apply output: %w", err)
			}
			if err := flushOutput(cmd.OutOrStdout()); err != nil {
				writeNotApplied(cmd.OutOrStdout(), presentation, err)
				return err
			}
			if err := service.Execute(ctx, validated); err != nil {
				writeNotApplied(cmd.OutOrStdout(), presentation, err)
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), presentation.notice("Applied — GitHub stack updated"))
			fmt.Fprintln(cmd.OutOrStdout(), presentation.subdued("Changes were made."))
			return nil
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "", "Graphite-tracked local branch to link (defaults to current branch)")
	cmd.Flags().StringVar(&trunk, "trunk", "", "Graphite-declared trunk to use as the link base")
	cmd.Flags().BoolVar(&apply, "apply", false, "invoke gh stack link after revalidation")
	_ = cmd.RegisterFlagCompletionFunc("branch", completionCallback(service.BranchCompletions))
	_ = cmd.RegisterFlagCompletionFunc("trunk", completionCallback(func(ctx context.Context, prefix string) ([]string, error) {
		return service.TrunkCompletions(ctx, branch, prefix)
	}))
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
	if _, err := fmt.Fprintf(writer, "%s: %s\n", presentation.accent(fmt.Sprintf("Resolved target (%s)", preview.TargetSource)), preview.Target); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(writer, presentation.accent("Link stack (bottom to top):")); err != nil {
		return err
	}
	for index, node := range preview.Nodes {
		if node.Trunk {
			if _, err := fmt.Fprintf(writer, "  %s\n", presentation.trunk(node.Branch+" (trunk)")); err != nil {
				return err
			}
			continue
		}
		label := fmt.Sprintf("%s (#%d)", node.Branch, node.PRNumber)
		if node.Unresolved != "" {
			label = fmt.Sprintf("%s (unresolved: %s)", node.Branch, node.Unresolved)
		}
		if _, err := fmt.Fprintf(writer, "%s└─ %s\n", strings.Repeat("  ", index), presentation.accent(label)); err != nil {
			return err
		}
	}
	if err := writeCommand(writer, preview.commandText(), presentation); err != nil {
		return err
	}
	if preview.ApplyBlocked {
		if _, err := fmt.Fprintln(writer, "Apply blocked: resolve every unresolved GitHub PR mapping first."); err != nil {
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

func writeNotApplied(writer io.Writer, presentation Presentation, err error) {
	fmt.Fprintln(writer, presentation.accent("Not applied")+": "+err.Error())
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

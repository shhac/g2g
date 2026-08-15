package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shhac/gt2gh/internal/link"
	"github.com/shhac/gt2gh/internal/push"
)

func newPush(service push.Service, linkService link.Service, presentation Presentation) *cobra.Command {
	var remote string
	var selection stackOptions
	var apply bool
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Atomically push a Graphite stack's local refs (preview by default)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), linkTimeout)
			defer cancel()
			mode := "preview"
			if apply {
				mode = "apply"
			}
			ctx = commandContext(cmd, "push", mode, selection.branch, selection.trunk)
			plan, err := service.Plan(ctx, selection.Selection(), remote)
			if err != nil {
				return err
			}
			if !apply {
				if err := writePushPlan(cmd.OutOrStdout(), plan, presentation); err != nil {
					return err
				}
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "\n"+presentation.notice("No changes were made.")+" --apply re-discovers and revalidates before one atomic push.")
				return err
			}
			validated, err := service.Revalidate(ctx, selection.Selection(), remote, plan)
			if err != nil {
				return writeNotApplied(cmd.OutOrStdout(), presentation, err)
			}
			if err := writeReadyToPush(cmd.OutOrStdout(), validated, presentation); err != nil {
				return fmt.Errorf("render ready-to-apply output: %w", writeNotApplied(cmd.OutOrStdout(), presentation, err))
			}
			if err := flushOutput(cmd.OutOrStdout()); err != nil {
				return writeNotApplied(cmd.OutOrStdout(), presentation, err)
			}
			if err := service.Execute(ctx, validated); err != nil {
				return writeNotApplied(cmd.OutOrStdout(), presentation, err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "\n"+presentation.notice("Applied — remote refs updated atomically"))
			fmt.Fprintln(cmd.OutOrStdout(), presentation.subdued("Changes were made."))
			return nil
		},
	}
	selection.register(cmd, linkService, "Graphite-tracked local branch to push (defaults to current branch)", "Graphite-declared trunk to use as the push base")
	cmd.Flags().StringVar(&remote, "remote", "origin", "Git remote to push to")
	cmd.Flags().BoolVar(&apply, "apply", false, "atomically push with --force-with-lease after revalidation")
	return cmd
}

func writeReadyToPush(writer io.Writer, plan push.Plan, presentation Presentation) error {
	if _, err := fmt.Fprintln(writer, presentation.accent("Ready to apply")); err != nil {
		return err
	}
	return writePushPlan(writer, plan, presentation)
}

func writePushPlan(writer io.Writer, plan push.Plan, presentation Presentation) error {
	preview := newPushPreview(plan)
	if _, err := fmt.Fprintf(writer, "%s: %s\n\n", presentation.accent("Target"), presentation.branch(preview.Target)); err != nil {
		return err
	}
	for index, node := range preview.Nodes {
		if node.Trunk {
			if _, err := fmt.Fprintf(writer, "  %s\n", presentation.trunk(node.Branch+" (trunk)")); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintf(writer, "%s└─ %s\n", strings.Repeat("  ", index), presentation.branch(node.Branch)); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(writer); err != nil {
		return err
	}
	if err := writeCommand(writer, preview.commandText(), presentation); err != nil {
		return err
	}
	_, err := fmt.Fprintln(writer, presentation.subdued("Atomic push: all selected refs advance together or none do."))
	return err
}

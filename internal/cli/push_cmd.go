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
	var branch, trunk, remote string
	var noStack, apply bool
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
			ctx = commandContext(cmd, "push", mode, branch, trunk)
			selection := link.Selection{Branch: branch, Trunk: trunk, NoStack: noStack}
			plan, err := service.Plan(ctx, selection, remote)
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
			validated, err := service.Revalidate(ctx, selection, remote, plan)
			if err != nil {
				writeNotApplied(cmd.OutOrStdout(), presentation, err)
				return err
			}
			if err := writeReadyToPush(cmd.OutOrStdout(), validated, presentation); err != nil {
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
			fmt.Fprintln(cmd.OutOrStdout(), "\n"+presentation.notice("Applied — remote refs updated atomically"))
			fmt.Fprintln(cmd.OutOrStdout(), presentation.subdued("Changes were made."))
			return nil
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "", "Graphite-tracked local branch to push (defaults to current branch)")
	cmd.Flags().StringVar(&trunk, "trunk", "", "Graphite-declared trunk to use as the push base")
	cmd.Flags().StringVar(&remote, "remote", "origin", "Git remote to push to")
	cmd.Flags().BoolVar(&noStack, "no-stack", false, "stop at the selected branch instead of resolving the full linear stack")
	cmd.Flags().BoolVar(&apply, "apply", false, "atomically push with --force-with-lease after revalidation")
	_ = cmd.RegisterFlagCompletionFunc("branch", completionCallback(linkService.BranchCompletions))
	_ = cmd.RegisterFlagCompletionFunc("trunk", completionCallback(func(ctx context.Context, prefix string) ([]string, error) {
		return linkService.TrunkCompletions(ctx, branch, prefix)
	}))
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

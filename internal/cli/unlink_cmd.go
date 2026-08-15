package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/shhac/gt2gh/internal/link"
	"github.com/spf13/cobra"
)

// Unstacker is the explicit GitHub mutation dependency for unlink.
type Unstacker interface {
	Unstack(context.Context, int) error
}

func newUnlink(service link.Service, unstacker Unstacker, presentation Presentation) *cobra.Command {
	var selection stackOptions
	var apply bool
	var number int
	cmd := &cobra.Command{Use: "unlink", Short: "Remove a GitHub-native stack relationship (preview by default)", Args: cobra.NoArgs}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if number <= 0 {
			return fmt.Errorf("--stack-number is required; run g2g status to inspect the selected PR path, then supply the GitHub stack number")
		}
		mode := "preview"
		if apply {
			mode = "apply"
		}
		budgets := newBudgets(cmd)
		root := commandContext(cmd.Context(), cmd, "unlink", mode, selection.branch, selection.trunk)
		ctx, cancel := budgets.discovery(root)
		defer cancel()
		plan, err := service.PlanWithOptions(ctx, selection.Selection())
		if err != nil {
			return err
		}
		if len(plan.Issues) != 0 {
			return fmt.Errorf("unlink preview has unresolved PR mappings; repair them before applying")
		}
		if !apply {
			if err := writeUnlinkPlan(cmd.OutOrStdout(), plan, number, presentation); err != nil {
				return err
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), presentation.notice("No changes were made."))
			return err
		}
		validated, err := service.RevalidateWithOptions(ctx, selection.Selection(), plan)
		if err != nil {
			return writeNotApplied(cmd.OutOrStdout(), presentation, err)
		}
		if _, err := fmt.Fprintln(cmd.OutOrStdout(), presentation.accent("Ready to apply")); err != nil {
			return err
		}
		if err := writeUnlinkPlan(cmd.OutOrStdout(), validated, number, presentation); err != nil {
			return err
		}
		if err := flushOutput(cmd.OutOrStdout()); err != nil {
			return err
		}
		if unstacker == nil {
			return fmt.Errorf("GitHub stack unstack is not configured")
		}
		mutateCtx, cancelMutation := budgets.mutation(root, len(validated.Branches))
		defer cancelMutation()
		if err := unstacker.Unstack(mutateCtx, number); err != nil {
			return writeNotApplied(cmd.OutOrStdout(), presentation, mutationTimeout(err, "Run g2g status to see whether the relationship was removed."))
		}
		fmt.Fprintln(cmd.OutOrStdout(), presentation.notice("Unlinked — GitHub stack relationship removed"))
		fmt.Fprintln(cmd.OutOrStdout(), presentation.subdued("Branches and pull requests were unchanged."))
		return nil
	}
	cmd.Flags().IntVar(&number, "stack-number", 0, "GitHub stack number to unlink")
	selection.register(cmd, service, "Graphite-tracked local branch to inspect (defaults to current branch)", "Graphite-declared trunk to use as the base")
	cmd.Flags().BoolVar(&apply, "apply", false, "invoke gh stack unstack after revalidation")
	return cmd
}
func writeUnlinkPlan(w io.Writer, plan link.Plan, number int, p Presentation) error {
	if err := writeStatus(w, plan, p); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, p.subdued("This removes GitHub's stack relationship only. Branches and pull requests remain unchanged.")); err != nil {
		return err
	}
	return writeCommand(w, commandText([]string{"gh", "stack", "unstack", fmt.Sprint(number)}), p)
}

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
	var branch, trunk string
	var noStack, apply bool
	var number int
	cmd := &cobra.Command{Use: "unlink", Short: "Remove a GitHub-native stack relationship (preview by default)", Args: cobra.NoArgs}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if number <= 0 {
			return fmt.Errorf("--stack-number is required; run g2g status to inspect the selected PR path, then supply the GitHub stack number")
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), linkTimeout)
		defer cancel()
		mode := "preview"
		if apply {
			mode = "apply"
		}
		ctx = commandContext(cmd, "unlink", mode, branch, trunk)
		plan, err := service.PlanWithOptions(ctx, link.Selection{Branch: branch, Trunk: trunk, NoStack: noStack})
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
		validated, err := service.RevalidateWithOptions(ctx, link.Selection{Branch: branch, Trunk: trunk, NoStack: noStack}, plan)
		if err != nil {
			writeNotApplied(cmd.OutOrStdout(), presentation, err)
			return err
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
		if err := unstacker.Unstack(ctx, number); err != nil {
			writeNotApplied(cmd.OutOrStdout(), presentation, err)
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), presentation.notice("Unlinked — GitHub stack relationship removed"))
		fmt.Fprintln(cmd.OutOrStdout(), presentation.subdued("Branches and pull requests were unchanged."))
		return nil
	}
	cmd.Flags().IntVar(&number, "stack-number", 0, "GitHub stack number to unlink")
	cmd.Flags().StringVar(&branch, "branch", "", "Graphite-tracked local branch to inspect (defaults to current branch)")
	cmd.Flags().StringVar(&trunk, "trunk", "", "Graphite-declared trunk to use as the base")
	cmd.Flags().BoolVar(&noStack, "no-stack", false, "stop at the selected branch instead of resolving the full linear stack")
	cmd.Flags().BoolVar(&apply, "apply", false, "invoke gh stack unstack after revalidation")
	_ = cmd.RegisterFlagCompletionFunc("branch", completionCallback(service.BranchCompletions))
	_ = cmd.RegisterFlagCompletionFunc("trunk", completionCallback(func(ctx context.Context, prefix string) ([]string, error) {
		return service.TrunkCompletions(ctx, branch, prefix)
	}))
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

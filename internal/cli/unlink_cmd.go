package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/shhac/gt2gh/internal/githubstack"
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
		presentation := presentation.resolve(cmd)
		if cmd.Flags().Changed("stack-number") && number <= 0 {
			return fmt.Errorf("--stack-number must be a positive GitHub stack number")
		}
		mode := "preview"
		if apply {
			mode = "apply"
		}
		budgets := newBudgets(cmd)
		root := commandContext(cmd.Context(), cmd, "unlink", mode, selection.branch, selection.trunk)
		ctx, cancel := budgets.discovery(root)
		defer cancel()
		plan, err := service.Plan(ctx, selection.Selection())
		if err != nil {
			return err
		}
		if len(plan.Issues) != 0 {
			return fmt.Errorf("unlink preview has unresolved PR mappings; repair them before applying")
		}
		number, source, err := resolveStackNumber(number, plan)
		if err != nil {
			return err
		}
		if !apply {
			if err := writeUnlinkPlan(cmd.OutOrStdout(), plan, number, source, presentation); err != nil {
				return err
			}
			err := prose(cmd.OutOrStdout(), presentation, "\n"+presentation.notice("No changes were made."))
			return err
		}
		validated, err := service.Revalidate(ctx, selection.Selection(), plan)
		if err != nil {
			return writeNotApplied(cmd.OutOrStdout(), presentation, err)
		}
		if err := writeReadyBanner(cmd.OutOrStdout(), presentation); err != nil {
			return err
		}
		if err := writeUnlinkPlan(cmd.OutOrStdout(), validated, number, source, presentation); err != nil {
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
		prose(cmd.OutOrStdout(), presentation, presentation.notice("Unlinked — GitHub stack relationship removed"))
		prose(cmd.OutOrStdout(), presentation, presentation.subdued("Branches and pull requests were unchanged."))
		return nil
	}
	cmd.Flags().IntVar(&number, "stack-number", 0, "GitHub stack number to unlink (defaults to the one discovered on the selected path)")
	selection.register(cmd, service, "Graphite-tracked local branch to inspect (defaults to current branch)", "Graphite-declared trunk to use as the base")
	cmd.Flags().BoolVar(&apply, "apply", false, "invoke gh stack unstack after revalidation")
	return cmd
}

// resolveStackNumber picks the stack to unlink. status already reports native
// membership from the same batched read, so requiring the number by hand made
// the user copy a value the command had just discovered. An explicit
// --stack-number still wins, and discovery refuses rather than guesses when
// the selected path is not part of exactly one stack.
func resolveStackNumber(requested int, plan link.Plan) (int, string, error) {
	if requested > 0 {
		return requested, "--stack-number", nil
	}
	membership := githubstack.AssessMembership(plan.Branches, plan.PullRequests)
	switch membership.State {
	case githubstack.Unlinked:
		return 0, "", fmt.Errorf("the selected path is not linked into a GitHub stack; there is nothing to unlink")
	case githubstack.Conflicting:
		return 0, "", fmt.Errorf("the selected path spans conflicting GitHub stack membership; run g2g status, then pass --stack-number to choose deliberately")
	}
	return membership.StackNumber, "discovered on the selected path", nil
}

func writeUnlinkPlan(w io.Writer, plan link.Plan, number int, source string, p Presentation) error {
	view := statusView(plan)
	view.Operation = "unlink"
	view.Notes = nil
	view.Action = []string{"gh", "stack", "unstack", fmt.Sprint(number)}
	view = view.note(fmt.Sprintf("GitHub stack #%d · %s", number, source), severityNeutral)
	return writeStackView(w, view.note("This removes GitHub's stack relationship only. Branches and pull requests remain unchanged.", severityNeutral), p)
}

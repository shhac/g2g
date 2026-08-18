package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/link"
	"github.com/shhac/g2g/internal/stack"
	"github.com/spf13/cobra"
)

// Unstacker is the explicit GitHub mutation dependency for unlink.
type Unstacker interface {
	Unstack(context.Context, int) error
}

func newUnlink(service link.Service, unstacker Unstacker, completions stack.Completions, guard func(context.Context) error, presentation Presentation) *cobra.Command {
	var selection stackOptions
	var apply bool
	var number int
	cmd := &cobra.Command{Use: "unlink", GroupID: groupPublish, Short: "Remove a GitHub-native stack relationship (preview by default)", Args: cobra.NoArgs}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		presentation := presentation.resolve(cmd)
		if err := selection.validate(); err != nil {
			return err
		}
		if cmd.Flags().Changed("stack-number") && number <= 0 {
			return fmt.Errorf("--stack-number must be a positive GitHub stack number")
		}
		root := commandContext(cmd.Context(), cmd, "unlink", applyMode(apply), selection.branch, selection.trunk)

		// Resolving the stack number is part of planning: it reads the same
		// discovery, and an unresolvable one must stop the command before
		// anything is rendered.
		resolved, source := 0, ""
		plan := func(ctx context.Context) (link.Plan, error) {
			plan, err := service.Plan(ctx, selection.Selection())
			if err != nil {
				return link.Plan{}, err
			}
			if len(plan.Issues) != 0 {
				return link.Plan{}, fmt.Errorf("unlink preview has unresolved PR mappings; repair them before applying")
			}
			if resolved, source, err = resolveStackNumber(number, plan); err != nil {
				return link.Plan{}, err
			}
			return plan, nil
		}
		flow := applyFlow[link.Plan]{
			plan: plan,
			revalidate: func(ctx context.Context, preview link.Plan) (link.Plan, error) {
				return service.Revalidate(ctx, selection.Selection(), preview)
			},
			render: func(w io.Writer, p link.Plan, presentation Presentation) error {
				return writeUnlinkPlan(w, p, resolved, source, presentation)
			},
			guard: guard,
			execute: func(ctx context.Context, _ link.Plan) error {
				if unstacker == nil {
					return fmt.Errorf("GitHub stack unstack is not configured")
				}
				return unstacker.Unstack(ctx, resolved)
			},
			branches: func(plan link.Plan) int { return len(plan.Branches) },
			notices: flowNotices{
				preview:  "Re-run with --apply to unlink.",
				applied:  "Unlinked — GitHub stack relationship removed",
				changed:  "Branches and pull requests were unchanged.",
				recovery: "Run g2g status to see whether the relationship was removed.",
			},
		}
		return flow.run(cmd, root, newBudgets(cmd), presentation, apply)
	}
	cmd.Flags().IntVar(&number, "stack-number", 0, "GitHub stack number to unlink (defaults to the one discovered on the selected path)")
	selection.register(cmd, completions, stack.ReadableSources, "local branch to inspect (defaults to current branch)", "trunk to use as the base")
	// A GitHub native stack is linear, so these are the two scopes that can
	// produce one. stack still refuses when it forks, naming the remedy.
	selection.registerScope(cmd, stack.ProjectScopes, stack.ScopeStack, scopeUsage("unlink", stack.ProjectScopes))
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
	view, _ := membershipView(plan, "unlink")
	view.Action = []string{"gh", "stack", "unstack", fmt.Sprint(number)}
	view = view.note(fmt.Sprintf("GitHub stack #%d · %s", number, source), severityNeutral)
	return writeStackView(w, view.note("This removes GitHub's stack relationship only. Branches and pull requests remain unchanged.", severityNeutral), p)
}

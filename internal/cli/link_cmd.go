package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/link"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
)

func newLink(service link.Service, completions stack.Completions, guard func(context.Context) error, presentation Presentation) *cobra.Command {
	var selection stackOptions
	var apply bool
	cmd := &cobra.Command{
		Use:   "link",
		Short: "Link a stack to GitHub's native stacks (preview by default)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			presentation := presentation.resolve(cmd)
			if err := selection.validate(); err != nil {
				return err
			}
			root := commandContext(cmd.Context(), cmd, applyMode(apply), selection.branch, selection.trunk)
			flow := applyFlow[link.Plan]{
				plan: func(ctx context.Context) (link.Plan, error) {
					return linkable(service.Plan(ctx, selection.Selection()))
				},
				revalidation: revalidation{"link", "link plan"},
				precheck:     service.RequireClean,
				settle: func(_ context.Context, plan link.Plan) (link.Plan, error) {
					return plan, linkSettled(plan)
				},
				render:   writeLinkPlan,
				guard:    guard,
				execute:  service.Execute,
				branches: func(plan link.Plan) int { return len(plan.Branches) },
				noOp:     link.Plan.NothingToLink,
				suggest:  func(plan link.Plan) string { return githubStatusNext(plan.Snapshot) },
				blocked: func(plan link.Plan) string {
					if len(plan.Issues) == 0 {
						return ""
					}
					return blockedReason(plan)
				},
				notices: flowNotices{
					preview:  "Rerun with --apply to link.",
					noOp:     "No changes were needed or made.",
					applied:  "Applied — GitHub stack updated",
					changed:  "Changes were made.",
					recovery: "Run g2g github status to see whether GitHub recorded the link.",
				},
			}
			return flow.run(cmd, root, newBudgets(cmd), presentation, apply)
		},
	}
	selection.register(cmd, completions, stack.ReadableSources, "local branch to link (defaults to current branch)", "trunk to use as the link base")
	// A GitHub native stack is linear, so these are the two scopes that can
	// produce one. stack still refuses when it forks, naming the remedy.
	selection.registerScope(cmd, shape.ProjectScopes, shape.ScopeStack, scopeUsage("link", shape.ProjectScopes))
	cmd.Flags().BoolVar(&apply, "apply", false, "invoke gh stack link after revalidation")
	return cmd
}

// linkable refuses a forked selection before anything is shown as a plan. The
// plan itself is shared with status, which reads a fork happily, so the
// refusal belongs to the command that projects it.
func linkable(plan link.Plan, err error) (link.Plan, error) {
	if err != nil {
		return link.Plan{}, err
	}
	return plan, plan.Snapshot.RequireLinear("link")
}

// linkSettled refuses a revalidated plan whose pull requests do not resolve
// one to a branch. It follows the comparison rather than preceding it, so a
// mapping that changed underneath is reported as the plan having changed.
func linkSettled(plan link.Plan) error {
	if len(plan.Issues) != 0 {
		return fmt.Errorf("link preview has unresolved GitHub PR mappings; fix them and rerun before --apply")
	}
	return nil
}

func writeLinkPlan(writer io.Writer, plan link.Plan, presentation Presentation) error {
	return writeStackView(writer, linkView(plan), presentation)
}

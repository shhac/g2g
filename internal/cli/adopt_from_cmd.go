package cli

import (
	"context"
	"io"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/align"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
)

// adoptFromFlow is the half of adopting from another record that does not
// depend on which record: the graph write, the guard, and the view.
func adoptFromFlow(service align.Service, guard func(context.Context) error) applyFlow[align.AdoptPlan] {
	return applyFlow[align.AdoptPlan]{
		render: func(writer io.Writer, plan align.AdoptPlan, p Presentation) error {
			return writeStackView(writer, adoptFromView(plan), p)
		},
		guard:    guard,
		execute:  service.ApplyAdopt,
		branches: func(plan align.AdoptPlan) int { return len(plan.Adopt) },
		// Only whether there is no adoption work: the shared lifecycle gives
		// blocked plans their own refusal path.
		noOp:    func(plan align.AdoptPlan) bool { return len(plan.Adopt) == 0 },
		blocked: func(plan align.AdoptPlan) string { return plan.Blocked },
	}
}

// newGraphiteAdopt records what Graphite declares. Graphite's record is read
// whole, so it takes no selection at all.
func newGraphiteAdopt(service align.Service, guard func(context.Context) error, presentation Presentation) *cobra.Command {
	var apply bool
	cmd := &cobra.Command{
		Use:   "adopt",
		Short: "Record the stack Graphite declares in the g2g graph (preview by default)",
		Long: "Records what Graphite declares, so g2g can answer for those branches and restack them. " +
			"Adoption is the authority claim: g2g answers for every branch this adopts from then on, and " +
			"g2g status --from graphite is how to see Graphite's view of them again. " +
			"Nothing is written to Graphite, no branch is created, and nothing is removed from either record.",
		Args: cobra.NoArgs,
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		presentation := presentation.resolve(cmd)
		flow := adoptFromFlow(service, guard)
		flow.plan = service.PlanAdopt
		flow.revalidate = service.RevalidateAdopt
		flow.notices = flowNotices{
			preview:       "Rerun with --apply to adopt them.",
			noOp:          "Graphite declares nothing the g2g graph does not already record. Nothing to do.",
			applied:       "Adopted.",
			changed:       "Graphite still tracks these branches; g2g is what answers for them now.",
			recovery:      "The graph store may or may not have been written · rerun g2g graphite adopt to see what is left.",
			suggestedNext: "g2g status",
		}
		return flow.run(cmd, commandContext(cmd.Context(), cmd, applyMode(apply), "", ""), newBudgets(cmd), presentation, apply)
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "record the adoptions instead of previewing them")
	return cmd
}

// newGitHubAdopt records the stack a branch's open pull requests describe,
// which is how a stack somebody else published is picked up.
func newGitHubAdopt(service align.Service, completions stack.Completions, guard func(context.Context) error, presentation Presentation) *cobra.Command {
	var selection scopeOptions
	var apply bool
	cmd := &cobra.Command{
		Use:   "adopt",
		Short: "Record the stack its open pull requests describe in the g2g graph (preview by default)",
		Long: "Records the stack the current branch's open pull requests describe, or --branch's — a stack " +
			"someone else published, once its branches are here. Reading a pull request's base invokes gh, so " +
			"this needs the network. Branches this checkout does not have are refused rather than created.\n\n" +
			"Adoption is the authority claim: g2g answers for every branch this adopts from then on. " +
			"Nothing is written to GitHub, no branch is created, and nothing is removed from the g2g graph.",
		Args: cobra.NoArgs,
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		presentation := presentation.resolve(cmd)
		if err := selection.validateScope(); err != nil {
			return err
		}
		read := stack.Selection{Branch: selection.branch, Scope: selection.effectiveScope(), From: stack.SourceGitHub}
		flow := adoptFromFlow(service, guard)
		flow.plan = func(ctx context.Context) (align.AdoptPlan, error) {
			return service.PlanAdoptFromGitHub(ctx, read)
		}
		flow.revalidate = func(ctx context.Context, preview align.AdoptPlan) (align.AdoptPlan, error) {
			return service.RevalidateAdoptFromGitHub(ctx, read, preview)
		}
		flow.notices = flowNotices{
			preview:       "Rerun with --apply to adopt them.",
			noOp:          "The pull requests declare nothing the g2g graph does not already record. Nothing to do.",
			applied:       "Adopted.",
			changed:       "The pull requests are unchanged; g2g is what answers for these branches now.",
			recovery:      "The graph store may or may not have been written · rerun g2g github adopt to see what is left.",
			suggestedNext: "g2g status",
		}
		return flow.run(cmd, commandContext(cmd.Context(), cmd, applyMode(apply), selection.branch, ""), newBudgets(cmd), presentation, apply)
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "record the adoptions instead of previewing them")
	cmd.Flags().StringVar(&selection.branch, "branch", "", "the local branch whose stack to adopt (defaults to current branch)")
	_ = cmd.RegisterFlagCompletionFunc("branch", completionCallback(completions.Branches))
	selection.registerScope(cmd, align.GitHubAdoptScopes, shape.ScopeStack, scopeUsage("adopt", align.GitHubAdoptScopes))
	return cmd
}

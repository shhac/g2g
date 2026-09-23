package cli

import (
	"context"
	"io"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/align"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
)

// adoptOptions is which record an adoption reads and, for pull requests, how
// much of the stack. Graphite is read whole, so it takes no selection at all.
type adoptOptions struct {
	scopeOptions
	from string
}

func (o adoptOptions) validate() error {
	if o.from == align.FromGitHub {
		return o.validateScope()
	}
	return nil
}

// source completes the flow with the half that depends on the record read.
func (o adoptOptions) source(service align.Service, flow applyFlow[align.AdoptPlan]) applyFlow[align.AdoptPlan] {
	if o.from != align.FromGitHub {
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
		return flow
	}
	selection := stack.Selection{Branch: o.branch, Scope: o.effectiveScope(), From: stack.SourceGitHub}
	flow.plan = func(ctx context.Context) (align.AdoptPlan, error) {
		return service.PlanAdoptFromGitHub(ctx, selection)
	}
	flow.revalidate = func(ctx context.Context, preview align.AdoptPlan) (align.AdoptPlan, error) {
		return service.RevalidateAdoptFromGitHub(ctx, selection, preview)
	}
	flow.notices = flowNotices{
		preview:       "Rerun with --apply to adopt them.",
		noOp:          "The pull requests declare nothing the g2g graph does not already record. Nothing to do.",
		applied:       "Adopted.",
		changed:       "The pull requests are unchanged; g2g is what answers for these branches now.",
		recovery:      "The graph store may or may not have been written · rerun g2g github adopt to see what is left.",
		suggestedNext: "g2g status",
	}
	return flow
}

// adoptIsNoOp reports only whether there is no adoption work. The shared
// lifecycle separately gives blocked plans their canonical refusal path.
func adoptIsNoOp(plan align.AdoptPlan) bool { return len(plan.Adopt) == 0 }

// newAdoptFrom adopts what another record declares into the g2g graph. Its source
// is fixed by the namespace it sits in — `g2g graphite
// adopt`, `g2g github adopt` — so the command names the tool it reads rather
// than taking it as a flag.
func newAdoptFrom(service align.Service, from string, completions stack.Completions, guard func(context.Context) error, presentation Presentation) *cobra.Command {
	var apply bool
	options := adoptOptions{from: from}
	cmd := &cobra.Command{
		Use:   "adopt",
		Short: "Record the stack Graphite declares in the g2g graph (preview by default)",
		Long: "Records what Graphite declares, so g2g can answer for those branches and restack them. " +
			"Adoption is the authority claim: g2g answers for every branch this adopts from then on, and " +
			"--from graphite on a read is how to see Graphite's view of them again. " +
			"Nothing is written to Graphite, no branch is created, and nothing is removed from either record.",
		Args: cobra.NoArgs,
	}
	if from == align.FromGitHub {
		cmd.Short = "Record the stack its open pull requests describe in the g2g graph (preview by default)"
		cmd.Long = "Records the stack the current branch's open pull requests describe, or --branch's — a stack " +
			"someone else published, once its branches are here. Reading a pull request's base invokes gh, so " +
			"this needs the network. Branches this checkout does not have are refused rather than created.\n\n" +
			"Adoption is the authority claim: g2g answers for every branch this adopts from then on. " +
			"Nothing is written to GitHub, no branch is created, and nothing is removed from the g2g graph."
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		presentation := presentation.resolve(cmd)
		if err := options.validate(); err != nil {
			return err
		}
		ctx := commandContext(cmd.Context(), cmd, applyMode(apply), options.branch, "")
		flow := applyFlow[align.AdoptPlan]{
			render: func(writer io.Writer, plan align.AdoptPlan, p Presentation) error {
				return writeStackView(writer, adoptView(plan), p)
			},
			guard:    guard,
			execute:  service.ApplyAdopt,
			branches: func(plan align.AdoptPlan) int { return len(plan.Adopt) },
			noOp:     adoptIsNoOp,
			blocked:  func(plan align.AdoptPlan) string { return plan.Blocked },
		}
		flow = options.source(service, flow)
		return flow.run(cmd, ctx, newBudgets(cmd), presentation, apply)
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "record the adoptions instead of previewing them")
	if from == align.FromGitHub {
		cmd.Flags().StringVar(&options.branch, "branch", "", "the local branch whose stack to adopt (defaults to current branch)")
		_ = cmd.RegisterFlagCompletionFunc("branch", completionCallback(completions.Branches))
		options.registerScope(cmd, align.GitHubAdoptScopes, shape.ScopeStack, scopeUsage("adopt", align.GitHubAdoptScopes))
	}
	return cmd
}

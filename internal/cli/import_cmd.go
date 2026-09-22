package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/align"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
)

func newImport(service align.Service, completions stack.Completions, guard func(context.Context) error, presentation Presentation) *cobra.Command {
	var apply bool
	var options importOptions
	cmd := &cobra.Command{
		Use:     "import",
		GroupID: groupStructure,
		Short:   "Adopt the branches Graphite or open pull requests declare into the g2g graph (preview by default)",
		Long: "Records what another record declares, so g2g can answer for those branches and restack them.\n\n" +
			"--from graphite (the default) adopts everything Graphite declares. --from pull-request adopts the stack " +
			"the current branch's open pull requests describe, or --branch's — a stack someone else published, once " +
			"its branches are here. It is the only mode that needs the network, because reading a pull request's " +
			"base invokes gh; --branch and --scope apply to it alone.\n\n" +
			"Adoption is the authority claim, so g2g answers for every branch this adopts from then on, " +
			"and --from on a read becomes the only way to see the other record's view of them. " +
			"Nothing is written to Graphite or GitHub, no branch is created, and nothing is removed from either record.",
		Args: cobra.NoArgs,
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		presentation := presentation.resolve(cmd)
		if err := options.validate(); err != nil {
			return err
		}
		ctx := commandContext(cmd.Context(), cmd, "import", applyMode(apply), options.branch, "")
		flow := applyFlow[align.ImportPlan]{
			render: func(writer io.Writer, plan align.ImportPlan, p Presentation) error {
				return writeStackView(writer, importView(plan), p)
			},
			guard:    guard,
			execute:  service.ApplyImport,
			branches: func(plan align.ImportPlan) int { return len(plan.Adopt) },
			noOp:     importIsNoOp,
			blocked:  func(plan align.ImportPlan) string { return plan.Blocked },
		}
		flow = options.source(service, flow)
		return flow.run(cmd, ctx, newBudgets(cmd), presentation, apply)
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "record the adoptions instead of previewing them")
	cmd.Flags().StringVar(&options.from, "from", align.FromGraphite, "the record to adopt from: graphite, or pull-request (reads open pull requests with gh)")
	cmd.Flags().StringVar(&options.branch, "branch", "", "with --from pull-request, the local branch whose stack to adopt (defaults to current branch)")
	_ = cmd.RegisterFlagCompletionFunc("branch", completionCallback(completions.Branches))
	_ = cmd.RegisterFlagCompletionFunc("from", completionCallback(func(context.Context, string) ([]string, error) {
		return []string{align.FromGraphite, align.FromPullRequests}, nil
	}))
	options.registerScope(cmd, align.PullRequestImportScopes, shape.ScopeStack, "with --from pull-request, "+scopeUsage("adopt", align.PullRequestImportScopes))
	return cmd
}

// importOptions is which record an import reads and, for pull requests, how
// much of it. Graphite is read whole, so the selection flags mean nothing
// there and are refused rather than ignored.
type importOptions struct {
	scopeOptions
	from string
}

func (o importOptions) validate() error {
	switch o.from {
	case align.FromGraphite:
		if o.branch != "" || o.scope != "" {
			return fmt.Errorf("--branch and --scope choose a stack of pull requests · an import from Graphite adopts everything Graphite declares, so pass --from pull-request or drop them")
		}
		return nil
	case align.FromPullRequests:
		return o.validateScope()
	case string(stack.SourceG2G):
		return fmt.Errorf("there is nothing to import from g2g · the g2g graph is what import writes, so pass --from graphite or --from pull-request")
	default:
		return fmt.Errorf("unknown source %q · import reads %s or %s", o.from, align.FromGraphite, align.FromPullRequests)
	}
}

// source completes the flow with the half that depends on the record read.
func (o importOptions) source(service align.Service, flow applyFlow[align.ImportPlan]) applyFlow[align.ImportPlan] {
	if o.from != align.FromPullRequests {
		flow.plan = service.PlanImport
		flow.revalidate = service.RevalidateImport
		flow.notices = flowNotices{
			preview:       "Rerun with --apply to adopt them.",
			noOp:          "Graphite declares nothing the g2g graph does not already record. Nothing to do.",
			applied:       "Adopted.",
			changed:       "Graphite still tracks these branches; g2g is what answers for them now.",
			recovery:      "The graph store may or may not have been written · rerun g2g import to see what is left.",
			suggestedNext: "g2g graph",
		}
		return flow
	}
	selection := stack.Selection{Branch: o.branch, Scope: o.effectiveScope(), From: stack.SourcePullRequest}
	flow.plan = func(ctx context.Context) (align.ImportPlan, error) {
		return service.PlanImportFromPullRequests(ctx, selection)
	}
	flow.revalidate = func(ctx context.Context, preview align.ImportPlan) (align.ImportPlan, error) {
		return service.RevalidateImportFromPullRequests(ctx, selection, preview)
	}
	flow.notices = flowNotices{
		preview:       "Rerun with --apply to adopt them.",
		noOp:          "The pull requests declare nothing the g2g graph does not already record. Nothing to do.",
		applied:       "Adopted.",
		changed:       "The pull requests are unchanged; g2g is what answers for these branches now.",
		recovery:      "The graph store may or may not have been written · rerun g2g import --from pull-request to see what is left.",
		suggestedNext: "g2g graph",
	}
	return flow
}

// importIsNoOp reports only whether there is no adoption work. The shared
// lifecycle separately gives blocked plans their canonical refusal path.
func importIsNoOp(plan align.ImportPlan) bool { return len(plan.Adopt) == 0 }

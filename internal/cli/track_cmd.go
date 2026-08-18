package cli

import (
	"context"
	"io"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/graph"
)

// newTrack takes describesElsewhere so a blocked adoption can name the command
// that already knows the answer.
//
// It is a predicate rather than a reader on purpose: track reads Git alone, and
// the one thing it consults about Graphite is whether this repository uses it —
// a single file check, which is the documented exception to reading none of
// Graphite's paths. Asking Graphite what it records would run Graphite from a
// path whose independence from it is the feature.
func newTrack(service graph.Service, guard func(context.Context) error, describesElsewhere func(context.Context) (bool, error), presentation Presentation) *cobra.Command {
	var selection graphOptions
	var parent string
	var trunk string
	var wholeStack bool
	var apply bool
	cmd := &cobra.Command{
		Use:     "track",
		GroupID: groupStructure,
		Short:   "Record a branch's parent in the g2g-owned graph (preview by default)",
		Long: "Records where a branch sits, so every other command knows the structure.\n\n" +
			"--parent records one branch. --stack records the whole ancestry between a trunk and the " +
			"selected branch in one go, which is usually what a stack that already exists needs: " +
			"the order comes from commit ancestry, and it refuses rather than guessing where that is ambiguous.",
		Args: cobra.NoArgs,
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		presentation := presentation.resolve(cmd)
		ctx := commandContext(cmd.Context(), cmd, "track", applyMode(apply), selection.branch, trunk)
		// Whether another record already describes this repository. A failure to
		// answer is not worth reporting: the consequence is one missing
		// suggestion on a preview that already says what to do.
		elsewhere := false
		if describesElsewhere != nil {
			if described, err := describesElsewhere(ctx); err == nil {
				elsewhere = described
			}
		}
		if wholeStack {
			return trackStackFlow(service, selection, trunk, guard).run(cmd, ctx, newBudgets(cmd), presentation, apply)
		}
		flow := applyFlow[graph.TrackPlan]{
			plan: func(ctx context.Context) (graph.TrackPlan, error) {
				return service.PlanTrack(ctx, selection.Selection(), parent)
			},
			revalidate: func(ctx context.Context, preview graph.TrackPlan) (graph.TrackPlan, error) {
				return service.RevalidateTrack(ctx, selection.Selection(), parent, preview)
			},
			render: func(writer io.Writer, plan graph.TrackPlan, p Presentation) error {
				return writeGraphView(writer, trackView(plan, elsewhere), plan.Discovery, p)
			},
			guard:    guard,
			execute:  service.ApplyTrack,
			branches: func(plan graph.TrackPlan) int { return len(plan.Branches) },
			noOp:     trackIsNoOp,
			blocked:  func(plan graph.TrackPlan) string { return plan.Blocked },
			notices: flowNotices{
				preview:       "Rerun with --apply to record this edge.",
				noOp:          "The graph already records this parent. Nothing to do.",
				applied:       "Recorded.",
				changed:       "The g2g-owned graph now records this parent.",
				recovery:      "The graph store may or may not have been written.",
				suggestedNext: "g2g graph",
			},
		}
		return flow.run(cmd, ctx, newBudgets(cmd), presentation, apply)
	}
	cmd.Flags().StringVar(&parent, "parent", "", "branch to record as the parent (previewing the candidates when absent)")
	_ = cmd.RegisterFlagCompletionFunc("parent", completionCallback(parentCompletions(service, &selection)))
	cmd.Flags().BoolVar(&wholeStack, "stack", false, "record the whole ancestry between the trunk and the selected branch, not just one edge")
	cmd.Flags().StringVar(&trunk, "trunk", "", "where --stack stops (defaults to the only recorded root on the ancestry)")
	_ = cmd.RegisterFlagCompletionFunc("trunk", completionCallback(localBranchCompletions(service)))
	cmd.MarkFlagsMutuallyExclusive("parent", "stack")
	cmd.Flags().BoolVar(&apply, "apply", false, "write the recorded parent instead of previewing it")
	selection.registerBranch(cmd, service)
	return cmd
}

// trackStackFlow is the same safety sequence as every other mutating command,
// over the whole-ancestry plan rather than a single edge.
func trackStackFlow(service graph.Service, selection graphOptions, trunk string, guard func(context.Context) error) applyFlow[graph.StackPlan] {
	return applyFlow[graph.StackPlan]{
		plan: func(ctx context.Context) (graph.StackPlan, error) {
			return service.PlanStack(ctx, selection.Selection(), trunk)
		},
		revalidate: func(ctx context.Context, preview graph.StackPlan) (graph.StackPlan, error) {
			return service.RevalidateStack(ctx, selection.Selection(), trunk, preview)
		},
		render: func(writer io.Writer, plan graph.StackPlan, p Presentation) error {
			return writeGraphView(writer, trackStackView(plan), plan.Discovery, p)
		},
		guard:    guard,
		execute:  service.ApplyStack,
		branches: func(plan graph.StackPlan) int { return len(plan.Record) },
		noOp:     func(plan graph.StackPlan) bool { return len(plan.Record) == 0 },
		blocked:  func(plan graph.StackPlan) string { return plan.Blocked },
		notices: flowNotices{
			preview:       "Rerun with --apply to record this stack.",
			noOp:          "The graph already records this whole ancestry. Nothing to do.",
			applied:       "Recorded.",
			changed:       "The g2g-owned graph now records this stack.",
			recovery:      "The graph store may or may not have been written.",
			suggestedNext: "g2g graph",
		},
	}
}

// trackIsNoOp reports a plan that would rewrite the edge it already found.
func trackIsNoOp(plan graph.TrackPlan) bool {
	recorded, tracked := plan.Graph.Parent(plan.Target)
	return tracked && recorded == plan.Parent && plan.NewTrunk == ""
}

// parentCompletions offers the same ordered candidates the preview would, so
// completion and preview never disagree about what is on offer.
func parentCompletions(service graph.Service, selection *graphOptions) func(context.Context, string) ([]string, error) {
	return func(ctx context.Context, prefix string) ([]string, error) {
		plan, err := service.PlanTrack(ctx, selection.Selection(), "")
		if err != nil {
			return nil, err
		}
		branches := make([]string, 0, len(plan.Candidates))
		for _, candidate := range plan.Candidates {
			branches = append(branches, candidate.Branch)
		}
		return branches, nil
	}
}

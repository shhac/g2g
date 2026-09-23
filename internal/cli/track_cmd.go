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
	var apply bool
	cmd := &cobra.Command{
		Use:     "track",
		GroupID: groupShape,
		Short:   "Record which branch this one is stacked on (preview by default)",
		Long: "Records which branch a branch is stacked on — its parent — so every other command knows the " +
			"structure. It never chooses: without --parent it previews the candidates, nearest first, and stops.\n\n" +
			"This is not git's upstream tracking. To record a whole stack that already exists, use g2g adopt.",
		Args: cobra.NoArgs,
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		presentation := presentation.resolve(cmd)
		ctx := commandContext(cmd.Context(), cmd, applyMode(apply), selection.branch, "")
		flow := applyFlow[trackPlan]{
			plan: func(ctx context.Context) (trackPlan, error) {
				plan, err := service.PlanTrack(ctx, selection.Selection(), parent)
				return trackPlan{plan, describedElsewhere(ctx, describesElsewhere)}, err
			},
			revalidate: func(ctx context.Context, preview trackPlan) (trackPlan, error) {
				plan, err := service.RevalidateTrack(ctx, selection.Selection(), parent, preview.TrackPlan)
				return trackPlan{plan, describedElsewhere(ctx, describesElsewhere)}, err
			},
			render: func(writer io.Writer, plan trackPlan, p Presentation) error {
				return writeGraphView(writer, trackView(plan.TrackPlan, plan.elsewhere), plan.Discovery, p)
			},
			guard:    guard,
			execute:  func(ctx context.Context, plan trackPlan) error { return service.ApplyTrack(ctx, plan.TrackPlan) },
			branches: func(plan trackPlan) int { return len(plan.Branches) },
			noOp:     func(plan trackPlan) bool { return trackIsNoOp(plan.TrackPlan) },
			blocked:  func(plan trackPlan) string { return plan.Blocked },
			notices: flowNotices{
				preview:       "Rerun with --apply to record this edge.",
				noOp:          "The graph already records this parent. Nothing to do.",
				applied:       "Recorded.",
				changed:       "The g2g-owned graph now records this parent.",
				recovery:      "The graph store may or may not have been written.",
				suggestedNext: "g2g status",
			},
		}
		return flow.run(cmd, ctx, newBudgets(cmd), presentation, apply)
	}
	cmd.Flags().StringVar(&parent, "parent", "", "branch to record as the parent (previewing the candidates when absent)")
	_ = cmd.RegisterFlagCompletionFunc("parent", completionCallback(parentCompletions(service, &selection)))
	cmd.Flags().BoolVar(&apply, "apply", false, "write the recorded parent instead of previewing it")
	selection.registerBranch(cmd, service)
	return cmd
}

// trackPlan is the edge to record and whether another record already
// describes this repository, which is all a preview needs besides.
type trackPlan struct {
	graph.TrackPlan
	elsewhere bool
}

// describedElsewhere asks whether another record describes this repository. A
// failure to answer is not worth reporting: the consequence is one missing
// suggestion on a preview that already says what to do.
func describedElsewhere(ctx context.Context, describes func(context.Context) (bool, error)) bool {
	if describes == nil {
		return false
	}
	described, err := describes(ctx)
	return err == nil && described
}

// trackIsNoOp reports a plan that would rewrite the edge it already found.
func trackIsNoOp(plan graph.TrackPlan) bool {
	return plan.Updated.Equal(plan.Graph)
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

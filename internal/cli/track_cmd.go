package cli

import (
	"context"
	"errors"
	"io"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/githubstack"
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
	var trunk trunkFlags
	cmd := &cobra.Command{
		Use:     "track",
		GroupID: groupShape,
		Short:   "Record which branch this one is stacked on (preview by default)",
		Long: "Records which branch a branch is stacked on — its parent — so every other command knows the " +
			"structure. It never chooses: without --parent it previews the candidates, nearest first, and stops.\n\n" +
			"This is not git's upstream tracking. To record a whole stack that already exists, use g2g adopt.\n\n" +
			"--as-trunk records the branch as a trunk instead: it has no parent, so what is stacked on it lands into " +
			"it and it is never replayed. --into and --by say where it lands when it is finished, and how.",
		Args: cobra.NoArgs,
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		presentation := presentation.resolve(cmd)
		declaration, err := trunk.declaration(parent)
		if err != nil {
			return err
		}
		ctx := commandContext(cmd.Context(), cmd, applyMode(apply), selection.branch, "")
		if trunk.asTrunk {
			return declareFlow(service, selection, declaration, guard).run(cmd, ctx, newBudgets(cmd), presentation, apply)
		}
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
	trunk.register(cmd, localBranchCompletions(service))
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

// trunkFlags is what --as-trunk takes: whether, where it lands, and how.
type trunkFlags struct {
	asTrunk bool
	into    string
	by      string
}

func (f *trunkFlags) register(cmd *cobra.Command, branches func(context.Context, string) ([]string, error)) {
	cmd.Flags().BoolVar(&f.asTrunk, "as-trunk", false, "record the branch as a trunk, with no parent, instead of stacking it")
	cmd.Flags().StringVar(&f.into, "into", "", "with --as-trunk: the branch it lands into when it is finished")
	cmd.Flags().StringVar(&f.by, "by", "", "with --into: how it lands there: "+methodNames())
	_ = cmd.RegisterFlagCompletionFunc("into", completionCallback(branches))
	_ = cmd.RegisterFlagCompletionFunc("by", completionCallback(methodCompletions()))
}

// declaration checks the flags agree before anything is read. How a trunk
// lands has no default: a squash would collapse the very merges the shape
// usually exists to keep, so it is said rather than assumed.
func (f trunkFlags) declaration(parent string) (graph.Declaration, error) {
	switch {
	case !f.asTrunk && (f.into != "" || f.by != ""):
		return graph.Declaration{}, errors.New("--into and --by describe a trunk · add --as-trunk")
	case f.asTrunk && parent != "":
		return graph.Declaration{}, errors.New("--as-trunk and --parent are opposite answers: a trunk has no parent · pass one of them")
	case (f.into == "") != (f.by == ""):
		return graph.Declaration{}, errors.New("--into and --by go together: where it lands, and how")
	case f.by == "":
		return graph.Declaration{}, nil
	}
	method, err := githubstack.ParseMethod(f.by)
	if err != nil {
		return graph.Declaration{}, err
	}
	return graph.Declaration{Into: f.into, By: string(method)}, nil
}

// declareFlow is track's safety sequence over a declaration rather than an
// edge. It is its own plan because it answers a different question: not which
// parent, but that there is none.
func declareFlow(service graph.Service, selection graphOptions, declaration graph.Declaration, guard func(context.Context) error) applyFlow[graph.DeclarePlan] {
	return applyFlow[graph.DeclarePlan]{
		plan: func(ctx context.Context) (graph.DeclarePlan, error) {
			return service.PlanDeclare(ctx, selection.Selection(), declaration)
		},
		revalidate: func(ctx context.Context, preview graph.DeclarePlan) (graph.DeclarePlan, error) {
			return service.RevalidateDeclare(ctx, selection.Selection(), declaration, preview)
		},
		render: func(writer io.Writer, plan graph.DeclarePlan, p Presentation) error {
			return writeGraphView(writer, declareView(plan), plan.Discovery, p)
		},
		guard:    guard,
		execute:  service.ApplyDeclare,
		branches: func(plan graph.DeclarePlan) int { return len(plan.Branches) },
		noOp:     func(plan graph.DeclarePlan) bool { return plan.NoOp() },
		blocked:  func(plan graph.DeclarePlan) string { return plan.Blocked },
		notices: flowNotices{
			preview:       "Rerun with --apply to record this trunk.",
			noOp:          "The graph already records this trunk. Nothing to do.",
			applied:       "Recorded.",
			changed:       "The g2g-owned graph now records this trunk.",
			recovery:      "The graph store may or may not have been written.",
			suggestedNext: "g2g status",
		},
	}
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

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/retarget"
	"github.com/shhac/g2g/internal/stack"
)

func newRetarget(service retarget.Service, completions stack.Completions, guard func(context.Context) error, presentation Presentation) *cobra.Command {
	var selection stackOptions
	var apply bool
	cmd := &cobra.Command{
		Use:   "retarget",
		Short: "Point each pull request's base at the branch below it (preview by default)",
		Long: "Reconciles the base branch GitHub records for each pull request with the structure g2g resolved.\n\n" +
			"After a restack the local stack is correct and the remote bases may not be. This is the one command " +
			"that changes what a merge will do, which is why it is separate from submit and previews first.",
		Args: cobra.NoArgs,
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		presentation := presentation.resolve(cmd)
		if err := selection.validate(); err != nil {
			return err
		}
		root := commandContext(cmd.Context(), cmd, applyMode(apply), selection.branch, selection.trunk)
		flow := applyFlow[retarget.Plan]{
			plan: func(ctx context.Context) (retarget.Plan, error) {
				return service.Plan(ctx, selection.Selection())
			},
			revalidate: func(ctx context.Context, preview retarget.Plan) (retarget.Plan, error) {
				return service.Revalidate(ctx, selection.Selection(), preview)
			},
			render: func(writer io.Writer, plan retarget.Plan, p Presentation) error {
				return writeStackView(writer, retargetView(plan), p)
			},
			guard:    guard,
			execute:  service.Execute,
			branches: func(plan retarget.Plan) int { return len(plan.Changes) },
			noOp:     retarget.Plan.NothingToRetarget,
			blocked:  func(plan retarget.Plan) string { return plan.Blocked },
			suggest:  func(plan retarget.Plan) string { return githubStatusNext(plan.Snapshot) },
			// Bases already moved stay moved, so a run that fails on the
			// second is not "not applied".
			interrupted: func(_ context.Context, plan retarget.Plan, err error) (bool, error) {
				var stopped *retarget.Stopped
				if !errors.As(err, &stopped) {
					return false, nil
				}
				retry := selectedFrom(plan.Snapshot).aimedOr(retargetCommand) + " --apply"
				return true, stoppedMidRetarget(cmd, stopped, retry, presentation)
			},
			notices: flowNotices{
				preview:  "Rerun with --apply to move these bases.",
				noOp:     "Every pull request already sits on the branch below it. Nothing to do.",
				applied:  "Retargeted.",
				changed:  "GitHub now merges each pull request into the branch below it.",
				recovery: "Some bases may already have moved · run g2g github status to see which.",
			},
		}
		return flow.run(cmd, root, newBudgets(cmd), presentation, apply)
	}
	selection.register(cmd, completions, stack.ReadableSources, "local branch to retarget from (defaults to current branch)", "trunk to use as the base")
	// A GitHub native stack is linear, so these are the two scopes that can
	// produce one. stack still refuses when it forks, naming the remedy.
	selection.registerScopeOf(cmd, retargetCommand, "retarget")
	cmd.Flags().BoolVar(&apply, "apply", false, "move the bases instead of previewing the change")
	return cmd
}

// stoppedMidRetarget says which bases moved before the run failed. It makes no
// call of its own: the mutation budget may be what ran out.
func stoppedMidRetarget(cmd *cobra.Command, stopped *retarget.Stopped, retry string, p Presentation) error {
	numbers := make([]int, 0, len(stopped.Moved))
	for _, change := range stopped.Moved {
		numbers = append(numbers, change.Number)
	}
	moved := "Moved the base of " + pullRequestList(numbers) + ", and " + pick(len(numbers), "it stays", "they stay") + " moved."
	// A stop part-way prints nothing on stderr, so this is the only place
	// what gh said can be shown.
	writeDiagnostic(cmd.ErrOrStderr(), stopped)
	return writeStoppedPartWay(cmd.OutOrStdout(), p,
		fmt.Sprintf("Stopped part-way at #%d: %s", stopped.Failed.Number, stopped.Err),
		moved+" Rerun "+runnable(retry)+" to finish; it moves only the bases still wrong.",
		stopped)
}

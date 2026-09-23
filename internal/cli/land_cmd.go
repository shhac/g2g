package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/comment"
	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/land"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
)

func newLand(service land.Service, comments comment.Service, completions stack.Completions, guard func(context.Context) error, presentation Presentation) *cobra.Command {
	var selection stackOptions
	var options = land.Defaults()
	var method string
	var apply bool
	// The three cleanups are on, and each is declared as its own negative flag
	// rather than a default-true boolean with a second --no- spelling beside
	// it. One flag, one spelling, and the help line says what passing it does.
	var noDeleteRemote, noDeleteLocal, noForget, noComment bool

	cmd := &cobra.Command{
		Use:     "land",
		GroupID: groupPublish,
		Short:   "Squash-merge a stack down onto its trunk, bottom first (preview by default)",
		Args:    cobra.NoArgs,
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		presentation := presentation.resolve(cmd)
		if err := selection.validate(); err != nil {
			return err
		}
		chosen, err := githubstack.ParseMethod(method)
		if err != nil {
			return err
		}
		options.Method = chosen
		options.DeleteRemote, options.DeleteLocal, options.Forget = !noDeleteRemote, !noDeleteLocal, !noForget
		options.Comment = !noComment && comments.Ready()
		if !options.Forget {
			// A branch left recorded under one that has merged and been
			// deleted makes every later status and every later replay measure
			// against a structure that is not there.
			return fmt.Errorf("--no-forget would leave the branches above each landed one recorded under a branch that no longer exists · drop the flag, or land them one at a time")
		}
		root := commandContext(cmd.Context(), cmd, "land", applyMode(apply), selection.branch, selection.trunk)
		flow := applyFlow[land.Plan]{
			plan: func(ctx context.Context) (land.Plan, error) {
				return service.Plan(ctx, selection.Selection(), options)
			},
			revalidate: func(ctx context.Context, preview land.Plan) (land.Plan, error) {
				return service.Revalidate(ctx, selection.Selection(), options, preview)
			},
			render: writeLandPlan,
			guard:  guard,
			execute: func(ctx context.Context, plan land.Plan) error {
				if err := service.Apply(ctx, plan); err != nil {
					return err
				}
				selections := make([]stack.Selection, 0, len(plan.Above))
				for _, above := range plan.Above {
					selections = append(selections, stack.Selection{Branch: above})
				}
				return keepComments(ctx, comments, plan.Options.Comment, selections...)
			},
			branches: func(plan land.Plan) int { return plan.Landing() },
			// Landing waits on GitHub between its calls, so the ordinary
			// per-branch ceiling would cut a merge off mid-flight.
			budget:  budgets.landing,
			noOp:    land.Plan.Nothing,
			blocked: func(plan land.Plan) string { return plan.Blocked },
			// A descent that stops part-way has landed everything below where
			// it stopped, and those merges stay. Reporting it as "not applied"
			// would be wrong about the thing that matters most. One that
			// stopped before changing anything is exactly "not applied", and
			// exits as the failure it is.
			interrupted: func(_ context.Context, err error) (bool, error) {
				return landInterrupted(cmd, err, presentation)
			},
			notices: flowNotices{
				preview:       "Rerun with --apply to land this stack.",
				noOp:          "Every branch here has already landed. Nothing to do.",
				applied:       "Landed.",
				changed:       "Pull requests were merged and branches removed.",
				recovery:      "Some branches may already have merged · run g2g status to see which.",
				suggestedNext: "g2g status",
			},
		}
		return flow.run(cmd, root, newBudgets(cmd), presentation, apply)
	}

	selection.register(cmd, completions, stack.ReadableSources, "branch to land up to (defaults to current branch)", "trunk to land onto")
	// path is the default rather than stack: standing in the middle of a stack
	// and typing land means "take it down as far as here", and stack would
	// merge everything above you as well.
	selection.registerScope(cmd, shape.ProjectScopes, shape.ScopePath, scopeUsage("land", shape.ProjectScopes))
	cmd.Flags().StringVar(&method, "method", string(githubstack.MethodSquash), "how to merge each pull request: "+methodNames())
	_ = cmd.RegisterFlagCompletionFunc("method", completionCallback(methodCompletions()))
	cmd.Flags().StringVar(&options.Remote, "remote", "origin", "Git remote the branches are published to")
	cmd.Flags().BoolVar(&options.Admin, "admin", false, "merge without waiting for required checks, which a replay restarts on every branch above the first")
	cmd.Flags().BoolVar(&noDeleteRemote, "no-delete-remote", false, "keep the published branch after its pull request merges")
	cmd.Flags().BoolVar(&noDeleteLocal, "no-delete-local", false, "keep the local branch after its pull request merges")
	// Registered only so that asking for it is answered with why not: a
	// branch left recorded under a landed, deleted one breaks every later
	// replay. It is hidden because a help line offering it would be offering
	// something that always refuses.
	cmd.Flags().BoolVar(&noForget, "no-forget", false, "refused: the branches above a landed one must be reparented")
	cmd.Flags().BoolVar(&noComment, "no-comment", false, "do not keep the stack comments on what remains afterwards")
	_ = cmd.Flags().MarkHidden("no-forget")
	cmd.Flags().BoolVar(&apply, "apply", false, "merge the stack instead of previewing the descent")
	return cmd
}

// methodCompletions offers exactly the methods the flag accepts, so completion
// can never propose a value the command would refuse.
func methodCompletions() func(context.Context, string) ([]string, error) {
	return func(context.Context, string) ([]string, error) {
		names := make([]string, 0, len(githubstack.Methods))
		for _, method := range githubstack.Methods {
			names = append(names, string(method))
		}
		return names, nil
	}
}

func methodNames() string {
	names := make([]string, 0, len(githubstack.Methods))
	for _, method := range githubstack.Methods {
		names = append(names, string(method))
	}
	return strings.Join(names, ", ")
}

// landInterrupted claims a descent that stopped having changed something, and
// leaves one that changed nothing to the ordinary failure path.
func landInterrupted(cmd *cobra.Command, err error, p Presentation) (bool, error) {
	if handled, report := commentsNotKept(cmd, err, p); handled {
		return true, report
	}
	var stopped *land.Stopped
	if !errors.As(err, &stopped) || !stopped.PartWay() {
		return false, nil
	}
	return true, stoppedMidLand(cmd, stopped, p)
}

// stoppedMidLand reports how far a descent got.
//
// It reads everything it needs from the error, making no call of its own: the
// context it is handed is the mutation budget, and when the reason it stopped
// is that budget expiring, it has already expired.
func stoppedMidLand(cmd *cobra.Command, stopped *land.Stopped, p Presentation) error {
	writer := cmd.OutOrStdout()
	landed := "Nothing merged."
	if len(stopped.Landed) != 0 {
		landed = "Merged " + branchList(stopped.Landed) + ", and they stay merged."
	}
	if len(stopped.Changed) != 0 {
		// What happened short of a merge is on the remote or in the graph too,
		// and "nothing merged" alone read as nothing having happened.
		landed += " Also " + branchList(stopped.Changed) + "."
	}
	if len(stopped.Tidied) != 0 {
		landed += " Cleaned up after " + branchList(stopped.Tidied) + ", which had already landed."
	}
	if err := prose(writer, p, "\n"+p.problem("Stopped part-way at "+stopped.Branch+": "+stopped.Err.Error())); err != nil {
		return err
	}
	if err := prose(writer, p, p.subdued(landed+" Rerun "+runnable("g2g land")+" to see what is left.")); err != nil {
		return err
	}
	// The merges that happened are permanent, so this is not a failure to
	// retry — but it is not what was asked for either, and a script reading
	// only the status had no way to tell the two apart.
	return stoppedPartWay(stopped)
}

package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/comment"
	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/land"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
)

func newLand(service land.Service, comments comment.Service, completions stack.Completions, guard func(context.Context) error, presentation Presentation) *cobra.Command {
	flags := landOptions{options: land.Defaults()}

	cmd := &cobra.Command{
		Use:     "land",
		GroupID: groupPublish,
		Short:   "Squash-merge a stack down onto its trunk, bottom first (preview by default)",
		Args:    cobra.NoArgs,
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		presentation := presentation.resolve(cmd)
		options, err := flags.chosen(cmd, comments)
		if err != nil {
			return err
		}
		root := commandContext(cmd.Context(), cmd, applyMode(flags.apply), flags.selection.branch, flags.selection.trunk)
		flow := landFlow(cmd, service, comments, flags.selection.Selection(), options, guard, presentation)
		return flow.run(cmd, root, newBudgets(cmd), presentation, flags.apply)
	}

	flags.selection.register(cmd, completions, stack.ReadableSources, "branch to land up to (defaults to current branch)", "trunk to land onto")
	// path is the default rather than stack: standing in the middle of a stack
	// and typing land means "take it down as far as here", and stack would
	// merge everything above you as well.
	flags.selection.registerScope(cmd, shape.ProjectScopes, shape.ScopePath, scopeUsage("land", shape.ProjectScopes))
	cmd.Flags().StringVar(&flags.method, "method", string(githubstack.MethodSquash), "how to merge each pull request: "+methodNames())
	_ = cmd.RegisterFlagCompletionFunc("method", completionCallback(methodCompletions()))
	cmd.Flags().StringVar(&flags.options.Remote, "remote", localgit.DefaultRemote, "Git remote the branches are published to")
	cmd.Flags().BoolVar(&flags.options.Admin, "admin", false, "merge without waiting for required checks, which a replay restarts on every branch above the first")
	cmd.Flags().BoolVar(&flags.noDeleteRemote, "no-delete-remote", false, "keep the published branch after its pull request merges")
	cmd.Flags().BoolVar(&flags.noDeleteLocal, "no-delete-local", false, "keep the local branch after its pull request merges")
	registerNoSetUpstream(cmd, &flags.noSetUpstream)
	// Registered only so that asking for it is answered with why not: a
	// branch left recorded under a landed, deleted one breaks every later
	// replay. It is hidden because a help line offering it would be offering
	// something that always refuses.
	cmd.Flags().BoolVar(&flags.noForget, "no-forget", false, "refused: the branches above a landed one must be reparented")
	cmd.Flags().BoolVar(&flags.noComment, "no-comment", false, "do not keep the stack comments on what remains afterwards")
	_ = cmd.Flags().MarkHidden("no-forget")
	cmd.Flags().BoolVar(&flags.apply, "apply", false, "merge the stack instead of previewing the descent")
	return cmd
}

type landOptions struct {
	selection stackOptions
	options   land.Options
	method    string
	apply     bool
	// Cleanups default to on; each flag names the cleanup to leave out.
	noDeleteRemote, noDeleteLocal, noForget, noComment, noSetUpstream bool
}

// chosen normalizes the parsed flags without mutating their defaults.
func (o landOptions) chosen(cmd *cobra.Command, comments comment.Service) (land.Options, error) {
	if err := o.selection.validate(); err != nil {
		return land.Options{}, err
	}
	method, err := githubstack.ParseMethod(o.method)
	if err != nil {
		return land.Options{}, err
	}
	options := o.options
	options.Method, options.MethodChosen = method, cmd.Flags().Changed("method")
	options.DeleteRemote, options.DeleteLocal = !o.noDeleteRemote, !o.noDeleteLocal
	options.Comment = !o.noComment && comments.Ready()
	options.Upstream = upstreamFor(o.noSetUpstream)
	if o.noForget {
		// A branch recorded under one that has merged and been deleted makes
		// later status and replay measure against a structure that is gone.
		return land.Options{}, fmt.Errorf("--no-forget would leave the branches above each landed one recorded under a branch that no longer exists · drop the flag, or land them one at a time")
	}
	return options, nil
}

// landFlow composes landing and comment upkeep through the shared apply sequence.
func landFlow(cmd *cobra.Command, service land.Service, comments comment.Service, selection stack.Selection, options land.Options, guard func(context.Context) error, presentation Presentation) applyFlow[land.Plan] {
	return applyFlow[land.Plan]{
		plan: func(ctx context.Context) (land.Plan, error) {
			return service.Plan(ctx, selection, options)
		},
		same:         land.Plan.Equal,
		revalidation: revalidation{"land", "land plan"},
		render:       writeLandPlan,
		guard:        guard,
		execute: func(ctx context.Context, plan land.Plan) error {
			if err := service.Apply(ctx, plan); err != nil {
				return err
			}
			selections := make([]stack.Selection, 0, len(plan.Above))
			for _, above := range plan.Above {
				selections = append(selections, stack.Selection{Branch: above})
			}
			return keepComments(ctx, comments, plan.KeepsComments(), selections...)
		},
		branches: func(plan land.Plan) int { return plan.Landing() },
		// Landing waits on GitHub between its calls, so the ordinary
		// per-branch ceiling would cut a merge off mid-flight.
		budget:  budgets.landing,
		noOp:    land.Plan.Nothing,
		blocked: land.Plan.Blocked,
		// A descent that stops part-way has landed everything below where
		// it stopped, and those merges stay. Reporting it as "not applied"
		// would be wrong about the thing that matters most. One that
		// stopped before changing anything is exactly "not applied", and
		// exits as the failure it is.
		interrupted: func(_ context.Context, _ land.Plan, err error) (bool, error) {
			return landInterrupted(cmd, err, presentation)
		},
		// Not aimed at the selection: what it selected has merged and its
		// branches are gone.
		suggest: always[land.Plan]("g2g github status"),
		notices: flowNotices{
			preview:  "Rerun with --apply to land this stack.",
			noOp:     "Every branch here has already landed. Nothing to do.",
			applied:  "Landed.",
			changed:  "Pull requests were merged and branches removed.",
			recovery: "Some branches may already have merged · run g2g github status to see which.",
		},
	}
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
	// A stop part-way prints nothing on stderr, so this is the only place
	// what gh said can be shown.
	writeDiagnostic(cmd.ErrOrStderr(), stopped)
	// The merges that happened are permanent, so this is not a failure to
	// retry — but it is not what was asked for either, and a script reading
	// only the status had no way to tell the two apart.
	return writeStoppedPartWay(writer, p,
		"Stopped part-way at "+stopped.Branch+": "+stopped.Err.Error(),
		landed+" Rerun "+runnable("g2g land")+" to see what is left.",
		stopped)
}

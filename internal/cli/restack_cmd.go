package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/restack"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/words"
	"github.com/shhac/g2g/internal/worktree"
)

// restackOptions are the resume verbs, which mirror git rebase because the
// operation underneath genuinely is one and the muscle memory already exists.
type restackOptions struct {
	resume    bool
	abort     bool
	skip      bool
	onto      string
	absorb    bool
	apply     bool
	worktrees worktree.Policy
	// worktreesGiven is whether --worktrees was named, which is what the
	// resume verbs refuse: comparing against the default let an explicit
	// --worktrees follow through.
	worktreesGiven bool
	selector       graphOptions
}

func (o restackOptions) resuming() bool { return o.resume || o.abort || o.skip }

func newRestack(service restack.Service, presentation Presentation) *cobra.Command {
	var options restackOptions
	cmd := &cobra.Command{
		Use:     "restack",
		GroupID: groupUpdate,
		Short:   "Replay a stack's commits so its contents match its recorded structure (preview by default)",
		Args:    cobra.NoArgs,
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		presentation := presentation.resolve(cmd)
		options.worktreesGiven = cmd.Flags().Changed("worktrees")
		if err := options.validate(); err != nil {
			return err
		}
		ctx := commandContext(cmd.Context(), cmd, applyMode(options.apply), options.selector.branch, "")
		if options.resuming() {
			return runResume(cmd, ctx, service, options, presentation)
		}
		return runRestack(cmd, ctx, service, options, presentation)
	}
	cmd.Flags().BoolVar(&options.resume, "continue", false, "resume an interrupted restack once conflicts are resolved")
	cmd.Flags().BoolVar(&options.abort, "abort", false, "undo an interrupted restack, restoring every branch it touched")
	cmd.Flags().BoolVar(&options.skip, "skip", false, "abandon the commit an interrupted restack stopped on")
	cmd.Flags().StringVar(&options.onto, "onto", "", "move the selection onto a different base instead of its recorded parent")
	cmd.Flags().BoolVar(&options.absorb, "absorb", false, "keep commits the parent dropped instead of dropping them too")
	cmd.Flags().BoolVar(&options.apply, "apply", false, "perform the replay instead of previewing it")
	registerWorktrees(cmd, &options.worktrees)
	options.selector.registerBranch(cmd, service.Graph)
	// Rewriting defaults to the branches above the target: a conflict below it
	// is one the user may be deliberately deferring, and replaying it uninvited
	// is how restacking from the middle walks into it every time.
	options.selector.registerScope(cmd, shape.RewriteScopes, graph.ScopeSubtree, scopeUsage("replay", shape.RewriteScopes))
	return cmd
}

// validate rejects the combinations that cannot mean anything, rather than
// silently preferring one of them.
func (o restackOptions) validate() error {
	// restack rewrites history, so it must refuse any scope it did not offer.
	// The service parses the wider read set, which is correct for a read-only
	// discovery and would otherwise let this command replay another root's work.
	if err := o.selector.validateScope(); err != nil {
		return err
	}
	chosen := 0
	for _, set := range []bool{o.resume, o.abort, o.skip} {
		if set {
			chosen++
		}
	}
	if chosen > 1 {
		return fmt.Errorf("--continue, --abort, and --skip are mutually exclusive")
	}
	if chosen == 1 && (o.apply || o.onto != "" || o.absorb || o.worktreesGiven) {
		return fmt.Errorf("--continue, --abort, and --skip resume the restack already in progress and take no other options")
	}
	return nil
}

// runResume drives the verbs that act on an operation already under way. They
// mutate immediately: the plan they act on was previewed before it started.
func runResume(cmd *cobra.Command, ctx context.Context, service restack.Service, options restackOptions, p Presentation) error {
	budgets := newBudgets(cmd)
	ctx, cancel := budgets.mutation(ctx, 1)
	defer cancel()

	if options.abort {
		if err := service.Abort(ctx); err != nil {
			return err
		}
		return prose(cmd.OutOrStdout(), p, p.notice("Restack aborted. Every branch is back where it started."))
	}

	advance := service.Continue
	if options.skip {
		advance = service.Skip
	}
	if err := advance(ctx); err != nil {
		// Resuming can run straight into the next conflict, which is the
		// operation working rather than failing.
		return resumeError(cmd, ctx, service, err, p)
	}
	remaining, err := service.InProgress(ctx)
	if err != nil {
		return err
	}
	if remaining {
		return stopped(cmd, ctx, service, nil, p)
	}
	return prose(cmd.OutOrStdout(), p, p.notice("Restack complete."))
}

// resumeError distinguishes hitting the next conflict from genuinely failing.
func resumeError(cmd *cobra.Command, ctx context.Context, service restack.Service, err error, p Presentation) error {
	interrupted, checkErr := service.InProgress(ctx)
	if checkErr != nil || !interrupted {
		return err
	}
	return stopped(cmd, ctx, service, err, p)
}

// conflictReporter is the one thing stopped needs from the restack service.
// Naming it here rather than taking the whole service is the same
// consumer-defined-interface rule the rest of the repository follows, and it is
// what lets the no-conflict branch be exercised without standing up a
// seventeen-method Git.
type conflictReporter interface {
	Conflicted(context.Context) ([]string, error)
}

// replayState is what says whether a restack was left for the person to
// finish, and on which files: what a command that replays as one of its steps
// needs to report stopping in it.
type replayState interface {
	InProgress(context.Context) (bool, error)
	conflictReporter
}

// replayPaused reports a restack left waiting on the person, with the files
// it stopped on.
func replayPaused(ctx context.Context, replay replayState) ([]string, bool) {
	if replay == nil {
		return nil, false
	}
	if paused, err := replay.InProgress(ctx); err != nil || !paused {
		return nil, false
	}
	conflicted, _ := replay.Conflicted(ctx)
	return conflicted, true
}

// resumeAdvice is how a replay left journaled is finished, whichever command
// left it: the problem in a line, and the ways through it.
//
// A replay that stopped with no unmerged file has not hit a conflict, and
// saying it has sends the reader looking for something that is not there.
// land's copy of this said exactly that, having lost the distinction restack's
// had.
func resumeAdvice(conflicted []string) (problem, ways string) {
	if len(conflicted) == 0 {
		return "stopped part-way, with nothing left unmerged",
			"Inspect with " + runnable("git status") + ", then run " + runnable("g2g restack --continue") + ", or " + runnable("g2g restack --abort") + " to undo."
	}
	return "stopped on a conflict in " + words.List(conflicted),
		"Resolve those files, " + runnable("git add") + " them, then run " + runnable("g2g restack --continue") + ". Or " + runnable("g2g restack --abort") + " to undo."
}

// stopped reports an interrupted rewrite. With no unmerged file, what Git said
// is the only useful thing there is, so it is shown.
func stopped(cmd *cobra.Command, ctx context.Context, conflicts conflictReporter, cause error, p Presentation) error {
	if cause == nil {
		cause = fmt.Errorf("restack stopped part-way")
	}
	paths, err := conflicts.Conflicted(ctx)
	if err != nil {
		paths = nil
	}
	problem, ways := resumeAdvice(paths)
	_ = prose(cmd.OutOrStdout(), p, p.problem(strings.ToUpper(problem[:1])+problem[1:]+"."))
	if len(paths) == 0 {
		_ = prose(cmd.OutOrStdout(), p, p.subdued(cause.Error()))
	}
	return writeWhatStands(cmd, p, ways, cause)
}

// runRestack is the preview/apply sequence, driven by applyFlow like every
// other mutating command.
//
// It was written out by hand because a rewrite can legitimately stop part-way
// and the flow's contract was that a mutation either happened or did not. The
// interrupted hook is what made that no longer the reason, and the copy it
// justified had already drifted in two places: a timeout during the rewrite
// carried no recovery advice, and a blocked plan previewed identically to an
// empty one, so a refusal read as a clean result.
func runRestack(cmd *cobra.Command, ctx context.Context, service restack.Service, options restackOptions, p Presentation) error {
	selection := options.selector.Selection()
	flow := applyFlow[restack.Plan]{
		guard: restackGuard(service),
		plan: func(ctx context.Context) (restack.Plan, error) {
			return service.Plan(ctx, selection, restack.ToBranch(options.onto), options.absorb, restack.Pending{}, options.worktrees)
		},
		revalidation: revalidation{"restack", "restack plan"},
		precheck:     service.RequireClean,
		render: func(w io.Writer, plan restack.Plan, p Presentation) error {
			return writeStackView(w, restackView(plan), p)
		},
		execute:  func(ctx context.Context, plan restack.Plan) error { return service.Apply(ctx, plan) },
		branches: func(plan restack.Plan) int { return len(plan.Steps) },
		// Not "no steps": a branch already on its --onto target has nothing to
		// replay and a new parent to record, and skipping Apply left the old one.
		noOp:     func(plan restack.Plan) bool { return plan.Nothing() },
		noOpSays: func(plan restack.Plan) string { return leftOnly(plan.Worktrees) },
		blocked:  restack.Plan.Blocked,
		// A restack reads no remote, so what it suggests compares with the
		// default one.
		suggest: func(plan restack.Plan) string { return replayNext(plan, "") },
		// A rewrite that stops on a conflict is half applied and resumable, so
		// "no changes were made" would be a lie. This is the case the whole
		// hook exists for.
		interrupted: func(ctx context.Context, _ restack.Plan, cause error) (bool, error) {
			if handled, report := worktreeNotBrought(cmd, cause, p); handled {
				return true, report
			}
			interrupted, err := service.InProgress(ctx)
			if err != nil || !interrupted {
				return false, nil
			}
			return true, stopped(cmd, ctx, service, cause, p)
		},
		notices: flowNotices{
			preview:  "Rerun with --apply to replay these commits.",
			noOp:     "Nothing to replay.",
			applied:  "Replayed.",
			changed:  "Branch contents now match the recorded structure.",
			recovery: "Inspect with git status, then g2g restack --continue or g2g restack --abort.",
		},
	}
	return flow.run(cmd, ctx, newBudgets(cmd), p, options.apply)
}

// restackGuard refuses a mutation while a restack is unfinished. A service
// that was never configured guards nothing, which is what keeps a root command
// built without one working exactly as before.
func restackGuard(service restack.Service) func(context.Context) error {
	if service.Journal == nil {
		return nil
	}
	return func(ctx context.Context) error {
		inProgress, err := service.InProgress(ctx)
		if err != nil {
			return err
		}
		if inProgress {
			return fmt.Errorf("%s", interruptedNote())
		}
		return nil
	}
}

package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/restack"
)

// restackOptions are the resume verbs, which mirror git rebase because the
// operation underneath genuinely is one and the muscle memory already exists.
type restackOptions struct {
	resume   bool
	abort    bool
	skip     bool
	onto     string
	absorb   bool
	apply    bool
	selector graphOptions
}

func (o restackOptions) resuming() bool { return o.resume || o.abort || o.skip }

func newRestack(service restack.Service, presentation Presentation) *cobra.Command {
	var options restackOptions
	cmd := &cobra.Command{
		Use:     "restack",
		GroupID: groupMaintain,
		Short:   "Replay a stack's commits so its contents match its recorded structure (preview by default)",
		Args:    cobra.NoArgs,
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		presentation := presentation.resolve(cmd)
		if err := options.validate(); err != nil {
			return err
		}
		ctx := commandContext(cmd.Context(), cmd, "restack", applyMode(options.apply), options.selector.branch, "")
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
	options.selector.registerBranch(cmd, service.Graph)
	// Rewriting defaults to the branches above the target: a conflict below it
	// is one the user may be deliberately deferring, and replaying it uninvited
	// is how restacking from the middle walks into it every time.
	options.selector.registerScope(cmd, graph.RewriteScopes, graph.ScopeSubtree, scopeUsage("replay", graph.RewriteScopes))
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
	if chosen == 1 && (o.apply || o.onto != "" || o.absorb) {
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

// stopped reports an interrupted rewrite.
//
// A rewrite that stops with no unmerged file has not hit a conflict, and
// saying it has sends the reader looking for something that is not there. In
// that case what Git said is the only useful thing we have.
func stopped(cmd *cobra.Command, ctx context.Context, conflicts conflictReporter, cause error, p Presentation) error {
	paths, err := conflicts.Conflicted(ctx)
	if err != nil || len(paths) == 0 {
		_ = prose(cmd.OutOrStdout(), p, p.problem("The rewrite stopped part-way, with nothing left unmerged."))
		if cause != nil {
			_ = prose(cmd.OutOrStdout(), p, p.subdued(cause.Error()))
		}
		return prose(cmd.OutOrStdout(), p, p.subdued("Inspect with git status, then run g2g restack --continue, or g2g restack --abort to undo."))
	}
	_ = prose(cmd.OutOrStdout(), p, p.problem("Stopped on a conflict in "+branchList(paths)+"."))
	return prose(cmd.OutOrStdout(), p, p.subdued("Resolve those files, git add them, then run g2g restack --continue. Or g2g restack --abort to undo."))
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
		plan: func(ctx context.Context) (restack.Plan, error) {
			return service.Plan(ctx, selection, options.onto, options.absorb)
		},
		revalidate: func(ctx context.Context, preview restack.Plan) (restack.Plan, error) {
			return service.Revalidate(ctx, selection, options.onto, options.absorb, preview)
		},
		render: func(w io.Writer, plan restack.Plan, p Presentation) error {
			return writeStackView(w, restackView(plan), p)
		},
		execute:  func(ctx context.Context, plan restack.Plan) error { return service.Apply(ctx, plan) },
		branches: func(plan restack.Plan) int { return len(plan.Steps) },
		// A blocked plan has no steps either, and calling that "nothing to
		// replay" would report a refusal as a clean result. Only an unblocked
		// plan with nothing in it is a no-op.
		noOp:    func(plan restack.Plan) bool { return plan.Blocked == "" && len(plan.Steps) == 0 },
		blocked: func(plan restack.Plan) string { return plan.Blocked },
		// A rewrite that stops on a conflict is half applied and resumable, so
		// "no changes were made" would be a lie. This is the case the whole
		// hook exists for.
		interrupted: func(ctx context.Context, cause error) (bool, error) {
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

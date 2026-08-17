package cli

import (
	"context"
	"fmt"

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
	options.selector.registerScope(cmd, graph.Scopes, "how much of the graph to replay: branch, path, subtree, or graph")
	return cmd
}

// validate rejects the combinations that cannot mean anything, rather than
// silently preferring one of them.
func (o restackOptions) validate() error {
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

// stopped reports an interrupted rewrite.
//
// A rewrite that stops with no unmerged file has not hit a conflict, and
// saying it has sends the reader looking for something that is not there. In
// that case what Git said is the only useful thing we have.
func stopped(cmd *cobra.Command, ctx context.Context, service restack.Service, cause error, p Presentation) error {
	paths, err := service.Conflicted(ctx)
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

// runRestack is the preview/apply sequence. It is written out rather than
// driven by applyFlow because a rewrite can legitimately stop part-way, and
// the flow's contract is that a mutation either happened or did not.
func runRestack(cmd *cobra.Command, ctx context.Context, service restack.Service, options restackOptions, p Presentation) error {
	budgets := newBudgets(cmd)
	discoveryCtx, cancelDiscovery := budgets.discovery(ctx)
	defer cancelDiscovery()

	selection := options.selector.Selection()
	plan, err := service.Plan(discoveryCtx, selection, options.onto, options.absorb)
	if err != nil {
		return err
	}
	if !options.apply {
		if err := writeStackView(cmd.OutOrStdout(), restackView(plan), p); err != nil {
			return err
		}
		if plan.Blocked != "" || len(plan.Steps) == 0 {
			return prose(cmd.OutOrStdout(), p, "\n"+p.notice("No changes were made."))
		}
		return prose(cmd.OutOrStdout(), p, "\n"+p.notice("No changes were made.")+" Rerun with --apply to replay these commits.")
	}

	validated, err := service.Revalidate(discoveryCtx, selection, options.onto, options.absorb, plan)
	if err != nil {
		return writeNotApplied(cmd.OutOrStdout(), p, err)
	}
	if validated.Blocked != "" {
		return writeNotApplied(cmd.OutOrStdout(), p, fmt.Errorf("%s", validated.Blocked))
	}
	if len(validated.Steps) == 0 {
		if err := writeStackView(cmd.OutOrStdout(), restackView(validated), p); err != nil {
			return err
		}
		return prose(cmd.OutOrStdout(), p, "\n"+p.notice("Nothing to replay."))
	}
	return applyRestack(cmd, ctx, budgets, service, validated, p)
}

// applyRestack renders and flushes the validated plan before the rewrite, so a
// reader always sees exactly what ran even if the process dies during it.
func applyRestack(cmd *cobra.Command, ctx context.Context, budgets budgets, service restack.Service, plan restack.Plan, p Presentation) error {
	if err := writeReadyBanner(cmd.OutOrStdout(), p); err != nil {
		return err
	}
	if err := writeStackView(cmd.OutOrStdout(), restackView(plan), p); err != nil {
		return err
	}
	if err := flushOutput(cmd.OutOrStdout()); err != nil {
		return writeNotApplied(cmd.OutOrStdout(), p, err)
	}

	mutateCtx, cancel := budgets.mutation(ctx, len(plan.Steps))
	defer cancel()
	if err := service.Apply(mutateCtx, plan); err != nil {
		// An interrupted rewrite is not a failure to apply: it is half applied
		// and resumable, and saying "no changes were made" would be a lie.
		interrupted, checkErr := service.InProgress(mutateCtx)
		if checkErr == nil && interrupted {
			return stopped(cmd, mutateCtx, service, err, p)
		}
		return writeNotApplied(cmd.OutOrStdout(), p, err)
	}
	_ = prose(cmd.OutOrStdout(), p, p.notice("Replayed."))
	return prose(cmd.OutOrStdout(), p, p.subdued("Branch contents now match the recorded structure."))
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

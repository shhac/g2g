package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// applyFlow is the preview/apply sequence every mutating command performs:
// discover, and on --apply re-discover, render the validated plan, flush it,
// and only then mutate exactly once.
//
// That ordering is this tool's core safety contract. It used to be written out
// once per command, which meant a reviewer had to verify it five times and the
// copies could drift apart — and they had, in spacing and in which of them
// short-circuited a no-op. Here it is stated once, and a command supplies only
// what genuinely differs.
type applyFlow[P any] struct {
	// plan discovers. revalidate re-discovers and compares against the preview,
	// returning an error if anything moved underneath.
	plan       func(context.Context) (P, error)
	revalidate func(context.Context, P) (P, error)
	render     func(io.Writer, P, Presentation) error
	execute    func(context.Context, P) error

	// branches sizes the mutation budget, which scales with the stack.
	branches func(P) int
	// noOp reports a plan that is valid but has nothing to do. Commands without
	// such a state leave it nil.
	noOp func(P) bool
	// wrapMutationError adds command-specific context to a failed mutation,
	// such as where a submission spec was retained.
	wrapMutationError func(error) error

	// guard refuses the whole command when another operation has left the
	// repository part-way through a rewrite. Mid-restack a branch may already
	// have moved while the graph still records where it used to be, so any
	// mutation would act on a structure that is currently untrue.
	guard func(context.Context) error

	notices flowNotices
}

// flowNotices are the human-facing lines around the sequence. They are the
// only presentational difference between the commands.
type flowNotices struct {
	// preview completes "No changes were made." in preview mode.
	preview string
	// noOp replaces it entirely when there is nothing to do.
	noOp string
	// applied and changed report a completed mutation.
	applied string
	changed string
	// recovery tells the reader what may already have happened if the mutation
	// phase runs out of time.
	recovery string
}

func (f applyFlow[P]) run(cmd *cobra.Command, root context.Context, budgets budgets, p Presentation, apply bool) error {
	ctx, cancel := budgets.discovery(root)
	defer cancel()

	if f.guard != nil {
		if err := f.guard(ctx); err != nil {
			return err
		}
	}
	plan, err := f.plan(ctx)
	if err != nil {
		return err
	}
	if !apply {
		return f.preview(cmd, plan, p)
	}

	validated, err := f.revalidate(ctx, plan)
	if err != nil {
		return writeNotApplied(cmd.OutOrStdout(), p, err)
	}
	if f.isNoOp(validated) {
		if err := f.render(cmd.OutOrStdout(), validated, p); err != nil {
			return err
		}
		return prose(cmd.OutOrStdout(), p, "\n"+p.notice(f.notices.noOp))
	}
	return f.mutate(cmd, root, budgets, validated, p)
}

func (f applyFlow[P]) preview(cmd *cobra.Command, plan P, p Presentation) error {
	if err := f.render(cmd.OutOrStdout(), plan, p); err != nil {
		return err
	}
	if f.isNoOp(plan) {
		return prose(cmd.OutOrStdout(), p, "\n"+p.notice(f.notices.noOp))
	}
	return prose(cmd.OutOrStdout(), p, "\n"+p.notice("No changes were made.")+" "+f.notices.preview)
}

// mutate renders and flushes the validated plan before invoking the mutation,
// so a reader always sees exactly what ran, even if the process dies during it.
func (f applyFlow[P]) mutate(cmd *cobra.Command, root context.Context, budgets budgets, validated P, p Presentation) error {
	if err := f.renderReady(cmd, validated, p); err != nil {
		return err
	}
	if err := flushOutput(cmd.OutOrStdout()); err != nil {
		return writeNotApplied(cmd.OutOrStdout(), p, err)
	}

	mutateCtx, cancelMutation := budgets.mutation(root, f.branches(validated))
	defer cancelMutation()
	if err := f.execute(mutateCtx, validated); err != nil {
		presented := writeNotApplied(cmd.OutOrStdout(), p, mutationTimeout(err, f.notices.recovery))
		if f.wrapMutationError != nil {
			return f.wrapMutationError(presented)
		}
		return presented
	}

	_ = prose(cmd.OutOrStdout(), p, p.notice(f.notices.applied))
	return prose(cmd.OutOrStdout(), p, p.subdued(f.notices.changed))
}

func (f applyFlow[P]) renderReady(cmd *cobra.Command, validated P, p Presentation) error {
	err := writeReadyBanner(cmd.OutOrStdout(), p)
	if err == nil {
		err = f.render(cmd.OutOrStdout(), validated, p)
	}
	if err == nil {
		return nil
	}
	// writeNotApplied both prints and marks the error as already presented, so
	// keep that call on its own line rather than nesting it inside a wrap where
	// the output it produces is invisible.
	presented := writeNotApplied(cmd.OutOrStdout(), p, err)
	return fmt.Errorf("render ready-to-apply output: %w", presented)
}

func (f applyFlow[P]) isNoOp(plan P) bool { return f.noOp != nil && f.noOp(plan) }

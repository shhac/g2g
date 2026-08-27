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

	// branches sizes the mutation budget, which scales with the stack. A
	// command that does not supply one gets the base budget rather than a
	// panic: every other closure here is optional and nil-guarded, and this one
	// being required-but-unchecked is not a distinction a caller can see.
	branches func(P) int
	// noOp reports a plan that is valid but has nothing to do. Commands without
	// such a state leave it nil.
	noOp func(P) bool
	// wrapMutationError adds command-specific context to a failed mutation,
	// such as where a submission spec was retained.
	wrapMutationError func(error) error
	// blocked reports why an already-validated plan must not be applied,
	// consulted after revalidation and before anything is announced. A refusal
	// has to come before the ready banner: a plan that cannot run should never
	// be introduced as one that is about to.
	blocked func(P) string
	// interrupted reports a mutation that stopped part-way rather than not
	// happening. It answers two separate questions: whether this failure was
	// one it claims, and whether writing its report succeeded.
	//
	// One return value cannot carry both. Every report helper in this package
	// returns nil on a successful write, so a hook that returned its report
	// directly said "not my case" precisely when it had handled the case — and
	// the flow then printed "Not applied" underneath the report and exited
	// non-zero.
	//
	// The flow's contract is that a mutation either happened or did not, which
	// is true of every command whose mutation is one call. It is not true of a
	// sequence that can stop between steps, and a command that grew its own
	// copy of this whole sequence to say so ended up skipping the revalidation
	// the copy did not include. One hook is cheaper than one copy.
	interrupted func(context.Context, error) (handled bool, err error)

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
	// suggestedNext is an optional, success-only continuation for a human who
	// has just completed a clear operation. It is deliberately separate from
	// recovery advice: it never repairs a blocked state, does not imply that it
	// must be followed, and is omitted when the command cannot know one safely.
	suggestedNext string
}

// planOutcome is the complete pre-mutation decision. Keeping the three cases
// named means command predicates only answer their own question: noOp means
// empty work, and blocked means refusal. The lifecycle owns their ordering.
type planOutcome uint8

const (
	planReady planOutcome = iota
	planNoOp
	planBlocked
)

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
	switch f.outcome(validated) {
	case planNoOp:
		if err := f.render(cmd.OutOrStdout(), validated, p); err != nil {
			return err
		}
		return prose(cmd.OutOrStdout(), p, "\n"+p.notice(f.notices.noOp))
	case planBlocked:
		return writeNotApplied(cmd.OutOrStdout(), p, fmt.Errorf("%s", f.blockedReason(validated)))
	default:
		return f.mutate(cmd, root, budgets, validated, p)
	}
}

func (f applyFlow[P]) preview(cmd *cobra.Command, plan P, p Presentation) error {
	if err := f.render(cmd.OutOrStdout(), plan, p); err != nil {
		return err
	}
	switch f.outcome(plan) {
	case planNoOp:
		return prose(cmd.OutOrStdout(), p, "\n"+p.notice(f.notices.noOp))
	case planBlocked:
		// A preview whose plan is already blocked must not close by inviting an
		// apply that will refuse. The rendered view names the reason, so repeating
		// it here would say it twice; what has to go is the invitation.
		return prose(cmd.OutOrStdout(), p, "\n"+p.notice("No changes were made.")+" Apply would refuse until that is resolved.")
	default:
		return prose(cmd.OutOrStdout(), p, "\n"+p.notice("No changes were made.")+" "+f.notices.preview)
	}
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

	mutateCtx, cancelMutation := budgets.mutation(root, f.mutationSize(validated))
	defer cancelMutation()
	if err := f.execute(mutateCtx, validated); err != nil {
		if f.interrupted != nil {
			if handled, report := f.interrupted(mutateCtx, err); handled {
				return report
			}
		}
		presented := writeNotApplied(cmd.OutOrStdout(), p, mutationTimeout(err, f.notices.recovery))
		if f.wrapMutationError != nil {
			return f.wrapMutationError(presented)
		}
		return presented
	}

	if err := prose(cmd.OutOrStdout(), p, p.notice(f.notices.applied)); err != nil {
		return err
	}
	if err := prose(cmd.OutOrStdout(), p, p.subdued(f.notices.changed)); err != nil {
		return err
	}
	return writeSuggestedNextStep(cmd.OutOrStdout(), p, f.notices.suggestedNext)
}

// writeSuggestedNextStep offers one likely continuation after a completed,
// unambiguous mutation. It is presentation-only: commands never run it or
// gather more facts to make it, and machine formats remain their exact
// plan/state documents.
func writeSuggestedNextStep(writer io.Writer, p Presentation, command string) error {
	if command == "" || p.machine() {
		return nil
	}
	return prose(writer, p, "\n"+p.accent("Suggested next step:")+" "+p.chip(command))
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

// outcome classifies an already-resolved plan before it is rendered ready or
// given a mutation budget. A refusal always wins over empty work.
func (f applyFlow[P]) outcome(plan P) planOutcome {
	if f.blockedReason(plan) != "" {
		return planBlocked
	}
	if f.noOp != nil && f.noOp(plan) {
		return planNoOp
	}
	return planReady
}

// blockedReason is why an apply would refuse, empty when it would proceed.
func (f applyFlow[P]) blockedReason(plan P) string {
	if f.blocked == nil {
		return ""
	}
	return f.blocked(plan)
}

// mutationSize is how many branches the mutation budget scales with.
func (f applyFlow[P]) mutationSize(plan P) int {
	if f.branches == nil {
		return 0
	}
	return f.branches(plan)
}

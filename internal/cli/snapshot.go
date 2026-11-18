package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/snapshot"
)

// snapshotting is what a command supplies to connect a preview to the apply
// that follows it. A preview writes its plan with what the plan rests on; an
// apply checks those things are still so and carries out that plan, without
// planning. See design-docs/snapshots.md.
type snapshotting[P any] struct {
	path string
	// tool, command and request say what made the plan and what it was asked,
	// so a plan is applied only by the g2g that made it, to the request it
	// answers.
	tool, command string
	request       []string
	git           snapshotGit
	store         graph.Store
	// rests is what the plan rests on.
	rests func(P) snapshot.Rests
	// held is the plan as it is written, and restore is the flow's plan from
	// what was read back. They differ where a command wraps the service's plan
	// with what only its own run knows.
	held    func(P) any
	restore func(json.RawMessage) (P, error)
	// still asks what the basis does not hold and an apply must know is
	// unchanged, such as whether a worktree the plan brings along is still
	// clean. Nil asks nothing.
	still func(context.Context, P) error
	// place refuses, before planning, a path the command could not apply
	// from: a restack needs a clean working tree, and a snapshot written
	// into it is a change. Nil accepts any path.
	place func(context.Context, string) error
}

// readPlan decodes a plan as strictly as it can be: a field the file has and
// this build's plan does not is a plan from another shape of g2g, and reading
// what is left of it would apply something nobody previewed.
func readPlan[T any](encoded json.RawMessage) (T, error) {
	var plan T
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	return plan, decoder.Decode(&plan)
}

type snapshotGit interface {
	snapshot.Reader
	snapshot.Ancestry
	WouldDirty(ctx context.Context, path string) (bool, error)
}

// registerSnapshot adds the flag to a command that offers it.
func registerSnapshot(cmd *cobra.Command, path *string) {
	cmd.Flags().StringVar(path, "snapshot", "", "write this preview's plan to a file, or with --apply carry out the plan in that file without planning again · refused if what the plan rests on has changed")
}

// requestOf is the flags a plan was shaped by, as they were given: every one
// the user set, less the ones that say how to run rather than what to plan.
func requestOf(cmd *cobra.Command) []string {
	request := make([]string, 0)
	visit := func(name, value string) {
		if !slices.Contains([]string{"apply", "snapshot", "timeout", "debug", "json", "porcelain", "no-links"}, name) {
			request = append(request, "--"+name+"="+value)
		}
	}
	cmd.Flags().Visit(func(flag *pflag.Flag) { visit(flag.Name, flag.Value.String()) })
	slices.Sort(request)
	return request
}

// runSnapshot is the flow for a command given --snapshot: a preview that
// writes the plan down, or an apply that carries out the one written.
func (f applyFlow[P]) runSnapshot(cmd *cobra.Command, root context.Context, budgets budgets, p Presentation, apply bool) error {
	ctx, cancel := budgets.discovery(root)
	defer cancel()
	if err := f.guardBefore(ctx); err != nil {
		return budgets.discoveryTimedOut(ctx, err)
	}
	if !apply {
		return budgets.discoveryTimedOut(ctx, f.previewSnapshot(ctx, cmd, p))
	}
	plan, err := f.heldPlan(ctx)
	if err != nil {
		return writeNotApplied(cmd.OutOrStdout(), p, budgets.discoveryTimedOut(ctx, err))
	}
	switch f.outcome(plan) {
	case planBlocked:
		return writeNotApplied(cmd.OutOrStdout(), p, fmt.Errorf("%s", f.blockedReason(plan)))
	case planNoOp:
		return prose(cmd.OutOrStdout(), p, p.notice(f.nothing(plan)))
	}
	cancel()
	return f.mutate(cmd, root, budgets, plan, p)
}

// previewSnapshot plans once and writes the plan with what it rests on, as
// the repository was when the plan was made.
//
// The repository is read before planning and again after. Planning can take
// minutes, and a branch that gains a commit after it was judged must not be
// written down as judged at its new tip, so the basis is cut from the first
// reading, and the second is only there to say the first still held.
func (f applyFlow[P]) previewSnapshot(ctx context.Context, cmd *cobra.Command, p Presentation) error {
	held := f.snapshot
	if err := snapshot.Writable(held.path); err != nil {
		return err
	}
	if held.place != nil {
		if err := held.place(ctx, held.path); err != nil {
			return err
		}
	}
	before, err := snapshot.Read(ctx, held.git, held.store)
	if err != nil {
		return err
	}
	plan, err := f.first(ctx)
	if err != nil {
		return err
	}
	if err := f.guardAfter(ctx, plan); err != nil {
		return err
	}
	if err := f.preview(cmd, plan, p); err != nil {
		return err
	}
	if f.outcome(plan) != planReady {
		// One left from an earlier preview holds a plan that is not this
		// one, and would be what the apply read.
		snapshot.Discard(held.path)
		return prose(cmd.OutOrStdout(), p, p.subdued("No snapshot was written: there is nothing here to apply."))
	}
	basis := before.Basis(held.rests(plan))
	after, err := snapshot.Read(ctx, held.git, held.store)
	if err != nil {
		return err
	}
	changed, err := basis.Changed(ctx, held.git, after)
	if err != nil {
		return err
	}
	if changed != "" {
		return fmt.Errorf("no snapshot was written, because the repository changed while this was being planned: %s · preview again", changed)
	}
	encoded, err := json.Marshal(held.held(plan))
	if err != nil {
		return err
	}
	if err := snapshot.Save(held.path, snapshot.File{Tool: held.tool, Command: held.command, Request: held.request, Basis: basis.Seen(after), Plan: encoded}); err != nil {
		return fmt.Errorf("write snapshot: %w", err)
	}
	again := append(append(strings.Fields(held.command), held.request...), "--snapshot", held.path, "--apply")
	return prose(cmd.OutOrStdout(), p, p.subdued("Snapshot written to "+held.path+" · "+runnable(repair.Command(again))+" carries out exactly this plan, if what it rests on has not changed."))
}

// heldPlan is the plan in the snapshot, once everything an apply checks
// before it would carry one out has passed.
func (f applyFlow[P]) heldPlan(ctx context.Context) (P, error) {
	var none P
	held := f.snapshot
	file, err := snapshot.Load(held.path)
	if err != nil {
		return none, err
	}
	if err := file.Answers(held.tool, held.command, held.request); err != nil {
		return none, err
	}
	plan, err := held.restore(file.Plan)
	if err != nil {
		return none, fmt.Errorf("%s holds a plan g2g cannot read · preview again: %w", held.path, err)
	}
	if err := f.guardAfter(ctx, plan); err != nil {
		return none, err
	}
	now, err := snapshot.Read(ctx, held.git, held.store)
	if err != nil {
		return none, err
	}
	changed, err := file.Basis.Changed(ctx, held.git, now)
	if err != nil {
		return none, err
	}
	diagnostic.Event(ctx, f.revalidation.event+".snapshot", diagnostic.Field{Key: "match", Value: fmt.Sprint(changed == "")}, diagnostic.Field{Key: "changed", Value: changed})
	if changed != "" {
		return none, fmt.Errorf("the repository is not as the plan in %s saw it: %s · preview again", held.path, changed)
	}
	if f.precheck != nil {
		if err := f.precheck(ctx); err != nil {
			return none, err
		}
	}
	if held.still != nil {
		if err := held.still(ctx, plan); err != nil {
			return none, err
		}
	}
	if f.blockedReason(plan) != "" || f.settle == nil {
		return plan, nil
	}
	return f.settle(ctx, plan)
}

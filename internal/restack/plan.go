// This file decides what a restack would do. Nothing in it changes the
// repository: every function here reads, measures, and reports.
package restack

import (
	"context"
	"fmt"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/graph"
)

// Plan works out what has to be replayed, without changing anything.
func (s Service) Plan(ctx context.Context, selection graph.Selection, onto Onto, absorb bool) (Plan, error) {
	if s.Git == nil || s.Journal == nil {
		return Plan{}, fmt.Errorf("restack service is not fully configured")
	}
	discovery, err := s.Graph.Discover(ctx, selection)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{Discovery: discovery, Onto: onto, Absorb: absorb}
	if blocked := s.blockedReason(discovery); blocked != "" {
		plan.Blocked = blocked
		return plan, nil
	}
	held, err := s.heldElsewhere(ctx, discovery.Branches)
	if err != nil {
		return Plan{}, err
	}
	if held != "" {
		plan.Blocked = held
		return plan, nil
	}
	steps, err := s.steps(ctx, discovery, onto.Object)
	if err != nil {
		return Plan{}, err
	}
	plan.Steps = steps
	if len(steps) == 0 {
		return plan, nil
	}
	if plan.Absorb && !plan.Absorbable() {
		plan.Blocked = "commits the parent dropped were rewritten rather than removed, so absorbing them would duplicate work the parent still carries"
		return plan, nil
	}
	updates, clean, predicted, err := s.preview(ctx, plan)
	if err != nil {
		return Plan{}, err
	}
	plan.Updates, plan.Clean = updates, clean
	plan.Predicted = predicted
	if predicted && !clean && !plan.chain() {
		// The resumable engine rewrites one line of descent per invocation, so
		// a conflicting fork would need several and a journal that tracks
		// which of them finished. Refusing is honest until it does.
		plan.Blocked = "this selection forks and the rewrite conflicts · restack one line at a time with --scope path"
	}
	diagnostic.Event(ctx, "restack.plan",
		diagnostic.Field{Key: "branches", Value: strings.Join(plan.Branches(), ",")},
		diagnostic.Field{Key: "clean", Value: fmt.Sprintf("%t", clean)},
		diagnostic.Field{Key: "orphans", Value: fmt.Sprint(len(plan.Orphaned()))},
	)
	return plan, nil
}

// blockedReason refuses any selection whose recorded structure cannot be
// trusted to describe what to replay.
func (s Service) blockedReason(discovery graph.Discovery) string {
	for _, branch := range discovery.Branches {
		state := discovery.States[branch]
		if state == graph.StateUntracked {
			// The root of a path is the base, not something to rewrite.
			continue
		}
		if !state.Restackable() {
			return fmt.Sprintf("%s is %s · retrack it before restacking", branch, state)
		}
		// An edge written before fork points were recorded says where the
		// branch hangs but not where its own commits begin. Standing in the
		// parent's tip for that is right only while the branch still sits on
		// it: once the parent moves, the substitute range is empty and the
		// rewrite silently becomes a no-op. Refuse and say so, rather than
		// report success having replayed nothing.
		if edge, tracked := discovery.Graph.Edges[branch]; tracked && edge.ForkPoint == "" && state != graph.StateAligned {
			return fmt.Sprintf("%s was recorded before fork points were · retrack it before restacking", branch)
		}
	}
	return ""
}

// steps builds the ordered rewrite, parents before children so each child is
// measured against the base its parent will actually have.
func (s Service) steps(ctx context.Context, discovery graph.Discovery, onto string) ([]Step, error) {
	steps := make([]Step, 0, len(discovery.Branches))
	// A branch whose parent is being rewritten has to be rewritten too, even
	// though it still sits exactly where its fork point says. Judging each
	// branch only against its parent's *current* tip restacks the bottom of a
	// stack and silently strands everything above it.
	rewriting := map[string]bool{}
	// landing records where a collapsed branch ends up, so its children are
	// measured against that rather than against a tip about to disappear.
	landing := map[string]string{}
	for _, branch := range discovery.Branches {
		edge, tracked := discovery.Graph.Edges[branch]
		if !tracked {
			continue
		}
		parent := edge.Parent
		if onto != "" && !discovery.Graph.Tracked(parent) {
			// Only the selection's own root is reparented; everything above it
			// keeps the structure that is already recorded.
			parent = onto
		}
		base, resolvedFork, tip, err := s.resolveStep(ctx, branch, parent, edge.ForkPoint)
		if err != nil {
			return nil, err
		}
		if resolvedFork == base && !rewriting[parent] {
			// Sitting where it belongs, under a parent that is not moving.
			continue
		}
		rewriting[branch] = true
		// A parent that collapsed onto its own base leaves this branch sitting
		// on that base instead, so measure against where the parent ends up.
		if landed, collapsed := landing[parent]; collapsed {
			base = landed
		}
		step := Step{Branch: branch, Parent: parent, Base: base, ForkPoint: resolvedFork, Tip: tip}
		if err := s.classifyOrphans(ctx, &step); err != nil {
			return nil, err
		}
		own, _, err := s.Git.Cherry(ctx, step.Base, branch, step.ForkPoint)
		if err != nil {
			return nil, err
		}
		// Nothing of this branch's own is missing from its new base, so it has
		// nothing left to replay and its ref simply moves there.
		step.Collapses = len(own) == 0
		if !step.Collapses {
			// Per-commit is not enough on the commonest way a branch lands. A
			// squash combines its commits into one, so that commit is
			// equivalent to none of them and each is offered to the engine
			// individually — where they conflict with the squashed version of
			// themselves. Asking of the whole branch at once is what sees it,
			// and collapsing here is what keeps a child's replay range from
			// starting below its parent's landed work.
			absorbed, err := s.Git.Absorbed(ctx, step.Base, branch)
			if err != nil {
				return nil, err
			}
			step.Collapses = absorbed
		}
		if step.Collapses {
			landing[branch] = step.Base
		}
		steps = append(steps, step)
	}
	return steps, nil
}

// resolveStep turns the names in an edge into the three objects a rewrite is
// decided from. An edge written before fork points were recorded behaves as
// though it forked at its parent's current tip.
func (s Service) resolveStep(ctx context.Context, branch, parent, forkPoint string) (base, fork, tip string, err error) {
	if base, err = s.Git.Resolve(ctx, parent); err != nil {
		return "", "", "", err
	}
	if forkPoint == "" {
		forkPoint = base
	}
	if fork, err = s.Git.Resolve(ctx, forkPoint); err != nil {
		return "", "", "", err
	}
	if tip, err = s.Git.Resolve(ctx, branch); err != nil {
		return "", "", "", err
	}
	return base, fork, tip, nil
}

// classifyOrphans records the commits the parent no longer has that this
// branch still carries, and whether keeping them would be coherent.
//
// A commit whose content still exists in the parent was rewritten, not
// dropped; absorbing it would give the branch a stale duplicate alongside the
// parent's new copy. Only a set where every orphan is genuinely gone can be
// absorbed.
func (s Service) classifyOrphans(ctx context.Context, step *Step) error {
	if step.Base == step.ForkPoint {
		step.Orphans = []string{}
		return nil
	}
	behind, err := s.Git.IsAncestor(ctx, step.ForkPoint, step.Base)
	if err != nil {
		return err
	}
	if behind {
		// The parent only moved forward, so it dropped nothing.
		step.Orphans = []string{}
		return nil
	}
	dropped, err := s.Git.CherryDropped(ctx, step.Base, step.ForkPoint)
	if err != nil {
		return err
	}
	_, total, err := s.Git.Divergence(ctx, step.Base, step.ForkPoint)
	if err != nil {
		return err
	}
	step.Orphans = dropped
	// Absorbing is coherent only when every orphan is genuinely gone. A set
	// that also contains rewritten commits would hand the branch stale copies
	// of work the parent still carries under new object ids.
	step.Absorbable = len(dropped) == total
	return nil
}

// preview asks the replay engine what the rewrite would produce, without
// producing it. A repository whose Git cannot replay gets no prediction, which
// costs the conflict warning but nothing else.
func (s Service) preview(ctx context.Context, plan Plan) (updates []localgit.RefUpdate, clean, predicted bool, err error) {
	supported, err := s.Git.SupportsReplay(ctx)
	if err != nil {
		return nil, false, false, err
	}
	if !supported {
		return nil, false, false, nil
	}
	ranges := plan.ranges()
	if len(ranges) == 0 {
		// Every step collapses: each branch's work is already in its new base
		// by content, so their refs move and nothing is replayed at all. There
		// is no range to predict and nothing that could conflict, and asking
		// the engine anyway failed the whole command with "no commit ranges
		// selected for replay" — on precisely the case where a stack has
		// finished landing.
		return nil, true, true, nil
	}
	updates, clean, err = s.Git.PreviewReplay(ctx, plan.Steps[0].Base, ranges)
	if err != nil {
		return nil, false, false, err
	}
	// Predicted means the preview actually ran, which the two returns above
	// already answer for the cases where it did not. Deriving it from the error
	// being returned alongside read as though a caller might see both, when the
	// only caller bails on the error first.
	return updates, clean, true, nil
}

// heldElsewhere refuses to rewrite a branch another worktree has checked out.
//
// A rewrite moves a ref without checking anything out, so nothing stopped it
// from moving a branch another worktree held. Git updated the ref; that
// worktree's index and working tree still described the old commit, so its next
// git status reported staged changes nobody made. The preview said "applies
// without touching your working tree or checked-out branch" while doing it,
// which was true of the worktree it ran in and false of the other.
//
// A Git too old to list worktrees, or a failure to ask, is not a reason to
// refuse a rewrite that was fine before this check existed.
func (s Service) heldElsewhere(ctx context.Context, branches []string) (string, error) {
	holder, ok := s.Git.(WorktreeReader)
	if !ok {
		return "", nil
	}
	elsewhere, err := holder.CheckedOutElsewhere(ctx)
	if err != nil || len(elsewhere) == 0 {
		return "", nil
	}
	held := make([]string, 0, len(branches))
	for _, branch := range branches {
		if path, taken := elsewhere[branch]; taken {
			held = append(held, fmt.Sprintf("%s (%s)", branch, path))
		}
	}
	if len(held) == 0 {
		return "", nil
	}
	return fmt.Sprintf("checked out in another worktree: %s · rewriting it there would leave that worktree describing a commit it no longer has · close it or narrow the selection", strings.Join(held, ", ")), nil
}

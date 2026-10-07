package restack

import (
	"context"
	"fmt"
	"slices"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/landed"
	"github.com/shhac/g2g/internal/repair"
)

// steps builds the ordered rewrite, parents before children so each child is
// measured against the base its parent will actually have.
func (s Service) steps(ctx context.Context, discovery graph.Discovery, onto Onto, pending Pending) ([]Step, error) {
	steps := make([]Step, 0, len(discovery.Branches))
	// A branch whose parent is being rewritten has to be rewritten too, even
	// though it still sits exactly where its fork point says. Judging each
	// branch only against its parent's *current* tip restacks the bottom of a
	// stack and silently strands everything above it.
	rewriting := map[string]bool{}
	// landing records where a collapsed branch ends up, so its children are
	// measured against that rather than against a tip about to disappear.
	landing := map[string]string{}
	placed := map[string]Step{}
	roots := selectionRoots(discovery)
	for _, branch := range discovery.Branches {
		edge, tracked := discovery.Graph.Edges[branch]
		if !tracked {
			continue
		}
		parent := edge.Parent
		if onto.Object != "" && slices.Contains(roots, branch) {
			// Only the selection's roots move; everything above them keeps the
			// structure that is already recorded.
			parent = onto.Object
		}
		base, resolvedFork, tip, err := s.resolveStep(ctx, branch, parent, edge.ForkPoint, pending)
		if err != nil {
			return nil, err
		}
		head := pending.at(branch, tip)
		if head != tip {
			if resolvedFork, err = s.pendingFork(ctx, branch, edge.Parent, base, resolvedFork, head); err != nil {
				return nil, err
			}
		}
		// Old records can start below trunk commits the branch already
		// contains. Those commits belong to the trunk, even if its local ref
		// was stale when tracked. This also covers upstream refs unavailable
		// at track time: pull has fetched the new base by the time we plan.
		advanced := false
		if forks, ok := s.Git.(graph.MergeBases); ok && !onto.Reparents() && discovery.Graph.IsTrunk(edge.Parent) {
			later, err := graph.LaterFork(ctx, s.Git, forks, base, head, resolvedFork)
			if err != nil {
				return nil, err
			}
			advanced, resolvedFork = later != resolvedFork, later
		}
		if resolvedFork == base && !rewriting[parent] && !advanced {
			// Sitting where it belongs, under a parent that is not moving.
			continue
		}
		rewriting[branch] = true
		// A parent that collapsed onto its own base leaves this branch sitting
		// on that base instead, so measure against where the parent ends up.
		if landed, collapsed := landing[parent]; collapsed {
			base = landed
		}
		step := Step{Branch: branch, Parent: parent, Base: base, ForkPoint: resolvedFork, Tip: tip, Head: head}
		if below, replayed := placed[parent]; replayed && !below.Collapses {
			contains, err := s.Git.IsAncestor(ctx, below.Head, head)
			if err != nil {
				return nil, err
			}
			step.Behind = !contains
		}
		if err := s.classifyOrphans(ctx, &step); err != nil {
			return nil, err
		}
		// Nothing of this branch's own is left to replay, so its ref simply
		// moves. Collapsing here is what keeps a child's replay range from
		// starting below its parent's landed work -- offered individually, a
		// squashed parent's commits conflict with the squashed version of
		// themselves.
		step.Collapses, err = landed.Into(ctx, s.Git, step.Base, head, step.ForkPoint)
		if err != nil {
			return nil, err
		}
		if step.Collapses {
			landing[branch] = step.Base
		}
		placed[branch] = step
		steps = append(steps, step)
	}
	return steps, nil
}

// resolveStep turns the names in an edge into the three objects a rewrite is
// decided from. An edge written before fork points were recorded behaves as
// though it forked at its parent's current tip.
func (s Service) resolveStep(ctx context.Context, branch, parent, forkPoint string, pending Pending) (base, fork, tip string, err error) {
	if base, err = s.Git.Resolve(ctx, parent); err != nil {
		return "", "", "", err
	}
	// Where the parent will be, which is not where Git says it is when the
	// caller is about to move it. The tip below is deliberately not overlaid:
	// it is what an abort restores this branch to, so it has to be where the
	// branch was before any of this ran.
	base = pending.at(parent, base)
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

// pendingFork is where the version of a branch a caller is about to put in
// place begins.
//
// The recorded fork point describes the version being replaced. When somebody
// restacked the branch onto a trunk that moved and published it, that fork
// point is still in the new version, but below the trunk commits it was
// replayed onto, so forkPoint..branch took those in: sync collected a stack
// that was already right and then replayed it a second time. A version that
// already sits on its parent's new tip begins there; one that still contains
// its recorded fork point, as a reviewer's commit on top does, begins there;
// and one that contains neither gives no range holding only its own commits,
// which is refused rather than guessed.
func (s Service) pendingFork(ctx context.Context, branch, parent, base, fork, head string) (string, error) {
	onParent, err := s.Git.IsAncestor(ctx, base, head)
	if err != nil {
		return "", err
	}
	if onParent {
		return base, nil
	}
	forked, err := s.Git.IsAncestor(ctx, fork, head)
	if err != nil {
		return "", err
	}
	if forked {
		return fork, nil
	}
	return "", unmeasured{branch: branch, parent: parent}
}

// unmeasured is a branch whose incoming version cannot be given a replay range.
type unmeasured struct{ branch, parent string }

func (u unmeasured) Error() string { return u.note().Sentence() }

func (u unmeasured) note() repair.Note {
	return repair.Note{
		Reason: fmt.Sprintf("the version of %s about to be taken is built on neither %s nor where %s was recorded as forking, so which commits are its own cannot be told", u.branch, u.parent, u.branch),
		Ways: []repair.Step{{
			Command: fmt.Sprintf("g2g track --branch %s --parent %s", repair.Quote(u.branch), repair.Quote(u.parent)),
			Effect:  "record where it forks, once it is built on its parent",
		}},
	}
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

// This file decides what a restack would do. Nothing in it changes the
// repository: every function here reads, measures, and reports.
package restack

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/shhac/gt2gh/internal/diagnostic"
	localgit "github.com/shhac/gt2gh/internal/git"
	"github.com/shhac/gt2gh/internal/graph"
)

// Git is the rewrite boundary. It is the only interface in gt2gh permitted to
// change commit history, and only through the two engines below.
type Git interface {
	graph.Ancestry
	Clean(context.Context) error
	SupportsReplay(context.Context) (bool, error)
	PreviewReplay(context.Context, string, []localgit.Range) ([]localgit.RefUpdate, bool, error)
	Replay(context.Context, string, []localgit.Range) error
	Rebase(context.Context, string, localgit.Range) error
	RebaseContinue(context.Context) error
	RebaseAbort(context.Context) error
	RebaseSkip(context.Context) error
	RebaseInProgress(context.Context) (bool, error)
	ConflictedPaths(context.Context) ([]string, error)
	ResetKeep(context.Context) error
	CherryDropped(context.Context, string, string) ([]string, error)
	Cherry(ctx context.Context, upstream, head, limit string) (absent, present []string, err error)
	PinForkPoint(context.Context, string, string) error
	UpdateBranch(context.Context, string, string) error
}

// Service rewrites stacks so their contents match their recorded structure.
type Service struct {
	Git     Git
	Graph   graph.Service
	Journal Journal
}

// Step is one branch's rewrite: replay its own commits onto a new base.
type Step struct {
	Branch string
	Parent string
	// Base is the object the branch will sit on.
	Base string
	// ForkPoint is where the branch's own commits begin. Base..Branch would be
	// the wrong range: it includes whatever the parent dropped or rewrote.
	ForkPoint string
	// Tip is the branch's current object, recorded so an abort can restore it.
	Tip string
	// Orphans are commits the parent no longer has that this branch still
	// carries, and Absorbable reports that every one of them was genuinely
	// dropped rather than rewritten.
	Orphans    []string
	Absorbable bool
	// Collapses means every commit this branch owns is already in its new
	// base by content, so it has nothing left to contribute and its ref simply
	// moves there.
	//
	// Handling this here rather than leaving it to the engines is what makes
	// them agree. Whether a rewrite drops an already-upstream commit or
	// reapplies it changed between Git 2.54 and 2.55, and it is exactly the
	// case a restack exists for, so the commits are never handed over at all.
	Collapses bool
}

// ranges are what the rewrite engines are given.
//
// Every range starts at the topmost step's fork point rather than at each
// branch's own. The engines replay the union of the ranges onto one base and
// update each named ref, so a chain has to be expressed as overlapping ranges
// from a single origin; per-branch origins ask them to place each branch
// directly on the base independently, which conflicts as soon as a branch
// depends on the one below it.
func (p Plan) ranges() []localgit.Range {
	rewriting := p.rewriting()
	if len(rewriting) == 0 {
		return nil
	}
	origin := rewriting[0].ForkPoint
	ranges := make([]localgit.Range, 0, len(rewriting))
	for _, step := range rewriting {
		ranges = append(ranges, localgit.Range{From: origin, To: step.Branch})
	}
	return ranges
}

// onto is the object the rewrite lands on: the base of the first step that
// still has commits, once any collapse below it has been accounted for.
func (p Plan) onto() string {
	rewriting := p.rewriting()
	if len(rewriting) == 0 {
		return ""
	}
	return rewriting[0].Base
}

// Replaying lists the branches whose commits are actually replayed, which is
// not every branch in the plan: one that collapses only has its ref moved.
func (p Plan) Replaying() []string {
	branches := make([]string, 0, len(p.Steps))
	for _, step := range p.rewriting() {
		branches = append(branches, step.Branch)
	}
	return branches
}

// rewriting is the steps that actually have commits to replay.
func (p Plan) rewriting() []Step {
	steps := make([]Step, 0, len(p.Steps))
	for _, step := range p.Steps {
		if !step.Collapses {
			steps = append(steps, step)
		}
	}
	return steps
}

// collapsing is the steps whose ref only has to move.
func (p Plan) collapsing() []Step {
	steps := make([]Step, 0)
	for _, step := range p.Steps {
		if step.Collapses {
			steps = append(steps, step)
		}
	}
	return steps
}

// reparenting is the branches this plan moves to a different parent, which is
// only ever the result of an explicit --onto.
func (p Plan) reparenting() map[string]string {
	moves := map[string]string{}
	for _, step := range p.Steps {
		if recorded, tracked := p.Graph.Parent(step.Branch); tracked && recorded != step.Parent {
			moves[step.Branch] = step.Parent
		}
	}
	return moves
}

// chain reports whether the steps form a single line of descent, which is the
// only shape the resumable engine can rewrite in one invocation.
func (p Plan) chain() bool {
	rewriting := p.rewriting()
	for index, step := range rewriting {
		if index == 0 {
			continue
		}
		if step.Parent != rewriting[index-1].Branch {
			return false
		}
	}
	return true
}

// Plan is a complete rewrite, ordered parents before children.
type Plan struct {
	graph.Discovery
	Onto   string
	Absorb bool
	Steps  []Step
	// Updates is what a replay says the refs would become, and Clean reports
	// that it would apply without a conflict. Both come from a preview that
	// moves nothing.
	Updates []localgit.RefUpdate
	Clean   bool
	// Predicted records that the preview actually ran. A Git too old to
	// replay cannot say anything in advance, and "we could not look" must not
	// be reported as "we looked and it will conflict".
	Predicted bool
	// Blocked is why an apply would refuse, empty when it would proceed.
	Blocked string
}

// Branches lists the branches this plan rewrites.
func (p Plan) Branches() []string {
	branches := make([]string, 0, len(p.Steps))
	for _, step := range p.Steps {
		branches = append(branches, step.Branch)
	}
	return branches
}

// Orphaned lists every commit a parent dropped that a child still carries.
func (p Plan) Orphaned() []string {
	orphans := make([]string, 0)
	for _, step := range p.Steps {
		orphans = append(orphans, step.Orphans...)
	}
	return orphans
}

// Absorbable reports whether every orphan was genuinely dropped, which is the
// only case where keeping them is coherent.
func (p Plan) Absorbable() bool {
	for _, step := range p.Steps {
		if len(step.Orphans) != 0 && !step.Absorbable {
			return false
		}
	}
	return len(p.Orphaned()) != 0
}

// Emptied lists branches the rewrite would leave with no commits of their own,
// because everything they carried is already in their new base.
//
// The comparison is against where the parent ends up, not where it is now: in
// a stack every branch above the bottom one is also being rewritten, so its
// current tip says nothing about the result.
// Emptied lists branches the rewrite leaves with no commits of their own,
// because everything they carried is already in their new base. It is known
// from the plan rather than inferred from a preview, so it is reported the
// same way on every Git.
func (p Plan) Emptied() []string {
	emptied := make([]string, 0)
	for _, step := range p.collapsing() {
		emptied = append(emptied, step.Branch)
	}
	return emptied
}

// Equal compares every fact that changes what the rewrite does.
func (p Plan) Equal(other Plan) bool {
	return p.Discovery.Equal(other.Discovery) &&
		p.Onto == other.Onto &&
		p.Absorb == other.Absorb &&
		p.Blocked == other.Blocked &&
		p.Clean == other.Clean &&
		p.Predicted == other.Predicted &&
		slices.EqualFunc(p.Steps, other.Steps, func(left, right Step) bool {
			return left.Branch == right.Branch && left.Parent == right.Parent &&
				left.Base == right.Base && left.ForkPoint == right.ForkPoint &&
				left.Tip == right.Tip && slices.Equal(left.Orphans, right.Orphans)
		})
}

// Plan works out what has to be replayed, without changing anything.
func (s Service) Plan(ctx context.Context, selection graph.Selection, onto string, absorb bool) (Plan, error) {
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
	steps, err := s.steps(ctx, discovery, onto)
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
	updates, clean, err = s.Git.PreviewReplay(ctx, plan.Steps[0].Base, plan.ranges())
	return updates, clean, err == nil, err
}

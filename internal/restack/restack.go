package restack

import (
	"context"
	"fmt"
	"maps"
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
	ResetKeep(context.Context) error
	CherryDropped(context.Context, string, string) ([]string, error)
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
}

// Range is the commits this step replays.
func (s Step) Range() localgit.Range { return localgit.Range{From: s.ForkPoint, To: s.Branch} }

// ranges are what the rewrite engines are given.
//
// Every range starts at the topmost step's fork point rather than at each
// branch's own. The engines replay the union of the ranges onto one base and
// update each named ref, so a chain has to be expressed as overlapping ranges
// from a single origin; per-branch origins ask them to place each branch
// directly on the base independently, which conflicts as soon as a branch
// depends on the one below it.
func (p Plan) ranges() []localgit.Range {
	if len(p.Steps) == 0 {
		return nil
	}
	origin := p.Steps[0].ForkPoint
	ranges := make([]localgit.Range, 0, len(p.Steps))
	for _, step := range p.Steps {
		ranges = append(ranges, localgit.Range{From: origin, To: step.Branch})
	}
	return ranges
}

// chain reports whether the steps form a single line of descent, which is the
// only shape the resumable engine can rewrite in one invocation.
func (p Plan) chain() bool {
	for index, step := range p.Steps {
		if index == 0 {
			continue
		}
		if step.Parent != p.Steps[index-1].Branch {
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
func (p Plan) Emptied() []string {
	resulting := make(map[string]string, len(p.Updates))
	for _, update := range p.Updates {
		resulting[update.Branch()] = update.New
	}
	emptied := make([]string, 0)
	for _, step := range p.Steps {
		tip, rewritten := resulting[step.Branch]
		if !rewritten {
			continue
		}
		base := step.Base
		if parentTip, moved := resulting[step.Parent]; moved {
			base = parentTip
		}
		if tip == base {
			emptied = append(emptied, step.Branch)
		}
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
	updates, clean, err := s.preview(ctx, plan)
	if err != nil {
		return Plan{}, err
	}
	plan.Updates, plan.Clean = updates, clean
	if !clean && !plan.chain() {
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
		if state == graph.StateUntracked && !discovery.Graph.Tracked(branch) {
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
		base, err := s.Git.Resolve(ctx, parent)
		if err != nil {
			return nil, err
		}
		forkPoint := edge.ForkPoint
		if forkPoint == "" {
			forkPoint = base
		}
		resolvedFork, err := s.Git.Resolve(ctx, forkPoint)
		if err != nil {
			return nil, err
		}
		tip, err := s.Git.Resolve(ctx, branch)
		if err != nil {
			return nil, err
		}
		if resolvedFork == base && !rewriting[parent] {
			// Sitting where it belongs, under a parent that is not moving.
			continue
		}
		rewriting[branch] = true
		step := Step{Branch: branch, Parent: parent, Base: base, ForkPoint: resolvedFork, Tip: tip}
		if err := s.classifyOrphans(ctx, &step); err != nil {
			return nil, err
		}
		steps = append(steps, step)
	}
	return steps, nil
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
func (s Service) preview(ctx context.Context, plan Plan) ([]localgit.RefUpdate, bool, error) {
	supported, err := s.Git.SupportsReplay(ctx)
	if err != nil {
		return nil, false, err
	}
	if !supported {
		return nil, false, nil
	}
	return s.Git.PreviewReplay(ctx, plan.Steps[0].Base, plan.ranges())
}

// Revalidate re-reads the world and refuses if anything moved since preview.
func (s Service) Revalidate(ctx context.Context, selection graph.Selection, onto string, absorb bool, preview Plan) (Plan, error) {
	if err := s.Git.Clean(ctx); err != nil {
		return Plan{}, err
	}
	plan, err := s.Plan(ctx, selection, onto, absorb)
	if err != nil {
		return Plan{}, err
	}
	if !plan.Equal(preview) {
		diagnostic.Event(ctx, "restack.revalidation", diagnostic.Field{Key: "match", Value: "false"})
		return Plan{}, fmt.Errorf("restack plan changed during revalidation; rerun without --apply to review the new plan")
	}
	diagnostic.Event(ctx, "restack.revalidation", diagnostic.Field{Key: "match", Value: "true"})
	return plan, nil
}

// Apply performs the rewrite. A plan the preview said applies cleanly is
// replayed without touching the checkout; anything else takes the resumable
// engine, which needs the user's working tree and says so first.
func (s Service) Apply(ctx context.Context, plan Plan) error {
	if plan.Blocked != "" {
		return fmt.Errorf("cannot restack: %s", plan.Blocked)
	}
	if len(plan.Steps) == 0 {
		return nil
	}
	if plan.Absorb {
		return s.absorb(ctx, plan)
	}
	if plan.Clean {
		return s.replay(ctx, plan)
	}
	return s.rebase(ctx, plan)
}

// absorb keeps the commits a parent dropped by re-recording where the branch
// forks, which needs no rewriting at all: the parent's tip is already an
// ancestor of the branch.
func (s Service) absorb(ctx context.Context, plan Plan) error {
	diagnostic.Event(ctx, "restack.absorb", diagnostic.Field{Key: "branches", Value: strings.Join(plan.Branches(), ",")})
	return s.recordForkPoints(ctx, plan.Discovery.Branches)
}

func (s Service) replay(ctx context.Context, plan Plan) error {
	ranges := plan.ranges()
	diagnostic.Event(ctx, "restack.replay", diagnostic.Field{Key: "branches", Value: strings.Join(plan.Branches(), ",")})
	if err := s.Git.Replay(ctx, plan.Steps[0].Base, ranges); err != nil {
		return err
	}
	// A replay moves refs without touching the index, so a user standing on a
	// rewritten branch would otherwise see changes they never made.
	if err := s.Git.ResetKeep(ctx); err != nil {
		return err
	}
	return s.recordForkPoints(ctx, plan.Discovery.Branches)
}

// rebase runs the resumable engine and journals enough to undo the whole
// operation, which git cannot do because it only restores the invocation it is
// running.
func (s Service) rebase(ctx context.Context, plan Plan) error {
	record := Record{
		Onto:     plan.Onto,
		Absorb:   plan.Absorb,
		Branch:   plan.Target,
		Scope:    string(plan.Scope),
		ReturnTo: plan.Target,
		Original: map[string]string{},
	}
	for _, step := range plan.Steps {
		record.Original[step.Branch] = step.Tip
	}
	if err := s.Journal.Save(ctx, record); err != nil {
		return err
	}
	diagnostic.Event(ctx, "restack.rebase", diagnostic.Field{Key: "branches", Value: strings.Join(plan.Branches(), ",")})
	last := plan.Steps[len(plan.Steps)-1]
	if err := s.Git.Rebase(ctx, plan.Steps[0].Base, localgit.Range{From: plan.Steps[0].ForkPoint, To: last.Branch}); err != nil {
		return err
	}
	return s.finish(ctx, graph.Selection{Branch: plan.Target, Scope: plan.Scope})
}

// recordForkPoints re-reads where each branch now forks and writes that back,
// so the next command measures against what the rewrite actually produced.
//
// It walks the whole selection rather than the plan's steps. Once a rewrite
// succeeds the re-derived plan has no steps left, so recording only those
// would record nothing at all and leave every fork point describing the world
// before the rewrite.
//
// A branch that is not actually built on its parent is skipped: writing its
// parent's tip as a fork point would assert a range that does not exist.
func (s Service) recordForkPoints(ctx context.Context, branches []string) error {
	adopted, err := s.Graph.Store.Load(ctx)
	if err != nil {
		return err
	}
	updated := adopted.Clone()
	for _, branch := range branches {
		edge, tracked := updated.Edges[branch]
		if !tracked {
			continue
		}
		built, err := s.Git.IsAncestor(ctx, edge.Parent, branch)
		if err != nil {
			return err
		}
		if !built {
			continue
		}
		forkPoint, err := s.Git.Resolve(ctx, edge.Parent)
		if err != nil {
			return err
		}
		edge.ForkPoint = forkPoint
		updated.Edges[branch] = edge
		if err := s.Git.PinForkPoint(ctx, branch, forkPoint); err != nil {
			return err
		}
	}
	return s.Graph.Store.Save(ctx, updated)
}

// Continue resumes an interrupted restack.
//
// It recomputes rather than replaying a stored queue, so a user who ran git
// rebase --continue or --abort themselves simply changes what work remains.
func (s Service) Continue(ctx context.Context) error {
	record, found, err := s.Journal.Load(ctx)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("no restack is in progress")
	}
	inProgress, err := s.Git.RebaseInProgress(ctx)
	if err != nil {
		return err
	}
	if inProgress {
		if err := s.Git.RebaseContinue(ctx); err != nil {
			return err
		}
	}
	return s.finish(ctx, record.Selection())
}

// Skip abandons the commit an interrupted rebase stopped on.
func (s Service) Skip(ctx context.Context) error {
	record, found, err := s.Journal.Load(ctx)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("no restack is in progress")
	}
	if err := s.Git.RebaseSkip(ctx); err != nil {
		return err
	}
	return s.finish(ctx, record.Selection())
}

// finish re-derives what is left. Anything still needing a rewrite is done
// now; when nothing is, the graph is brought up to date and the journal goes.
func (s Service) finish(ctx context.Context, selection graph.Selection) error {
	plan, err := s.Plan(ctx, selection, "", false)
	if err != nil {
		return err
	}
	if len(plan.Steps) != 0 && plan.Blocked == "" {
		if plan.Clean {
			if err := s.replay(ctx, plan); err != nil {
				return err
			}
			return s.Journal.Clear(ctx)
		}
		return s.Git.Rebase(ctx, plan.Steps[0].Base, plan.Steps[len(plan.Steps)-1].Range())
	}
	if err := s.recordForkPoints(ctx, plan.Discovery.Branches); err != nil {
		return err
	}
	return s.Journal.Clear(ctx)
}

// Abort restores every branch to the tip it had when the operation began,
// including paths that already completed.
func (s Service) Abort(ctx context.Context) error {
	record, found, err := s.Journal.Load(ctx)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("no restack is in progress")
	}
	inProgress, err := s.Git.RebaseInProgress(ctx)
	if err != nil {
		return err
	}
	if inProgress {
		if err := s.Git.RebaseAbort(ctx); err != nil {
			return err
		}
	}
	diagnostic.Event(ctx, "restack.abort", diagnostic.Field{Key: "branches", Value: strings.Join(slices.Sorted(maps.Keys(record.Original)), ",")})
	for _, branch := range slices.Sorted(maps.Keys(record.Original)) {
		if err := s.Git.UpdateBranch(ctx, branch, record.Original[branch]); err != nil {
			return err
		}
	}
	return s.Journal.Clear(ctx)
}

// InProgress reports an unfinished restack, which every other command has to
// refuse while it lasts: a branch may already have moved while the graph still
// records where it used to be.
func (s Service) InProgress(ctx context.Context) (bool, error) {
	if s.Journal == nil {
		return false, nil
	}
	_, found, err := s.Journal.Load(ctx)
	return found, err
}

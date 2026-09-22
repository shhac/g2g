// This file decides what a restack would do. Nothing in it changes the
// repository: every function here reads, measures, and reports.
package restack

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/landed"
	"github.com/shhac/g2g/internal/repair"
)

// Ready reports a service with everything it needs.
//
// One rule, called by both the guard below and the command registration in
// internal/cli. They were two hand-written conjunctions before, and three of
// them had already drifted -- a command could be registered and then refuse on
// use, or be hidden from a build that could have run it.
func (s Service) Ready() bool {
	return s.Git != nil && s.Journal != nil
}

// Plan works out what has to be replayed, without changing anything.
func (s Service) Plan(ctx context.Context, selection graph.Selection, onto Onto, absorb bool, pending Pending) (Plan, error) {
	if !s.Ready() {
		return Plan{}, fmt.Errorf("restack service is not fully configured")
	}
	discovery, err := s.Graph.Discover(ctx, selection)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{Discovery: discovery, Onto: onto, Absorb: absorb}
	if blocked := s.blockedReason(discovery); blocked.Reason != "" {
		plan.Repair = blocked
		plan.Blocked = blocked.Sentence()
		return plan, nil
	}
	if roots := selectionRoots(discovery); onto.Reparents() && len(roots) > 1 {
		// --onto moves one branch and what is stacked on it. Moving only the
		// first of several roots is what it used to do, silently.
		plan.Repair = ontoOneRoot(roots, onto.Parent)
		plan.Blocked = plan.Repair.Sentence()
		return plan, nil
	}
	steps, err := s.steps(ctx, discovery, onto.Object, pending)
	var unmeasurable unmeasured
	if errors.As(err, &unmeasurable) {
		plan.Repair = unmeasurable.note()
		plan.Blocked = plan.Repair.Sentence()
		return plan, nil
	}
	if err != nil {
		return Plan{}, err
	}
	// Only what will actually move can strand another worktree, so the check
	// waits for the steps. Asking it of the whole selection refused a path
	// restack because the trunk it never touches was checked out elsewhere.
	held, err := s.HeldElsewhere(ctx, moving(steps, absorb, pending))
	if err != nil {
		return Plan{}, err
	}
	if held.Reason != "" {
		plan.Repair, plan.Held = held, true
		plan.Blocked = held.Sentence()
		return plan, nil
	}
	plan.Steps = steps
	if len(steps) == 0 {
		return plan, nil
	}
	if plan.Absorb && !plan.Absorbable() {
		plan.Blocked = "commits the parent dropped were rewritten rather than removed, so absorbing them would duplicate work the parent still carries"
		return plan, nil
	}
	updates, clean, unpredicted, err := s.preview(ctx, plan)
	if err != nil {
		return Plan{}, err
	}
	plan.Updates, plan.Clean = updates, clean
	plan.Predicted, plan.Unpredicted = unpredicted == "", unpredicted
	if plan.Predicted && !clean && !plan.chain() {
		// The resumable engine rewrites one line of descent per invocation, so
		// a conflicting fork would need several and a journal that tracks
		// which of them finished. Refusing is honest until it does.
		plan.Lines = plan.leaves()
		ways := make([]repair.Step, 0, len(plan.Lines))
		for _, leaf := range plan.Lines {
			ways = append(ways, repair.Step{Command: "g2g restack --branch " + leaf + " --scope path", Effect: "rewrite the line of descent ending at " + leaf})
		}
		plan.Repair = repair.Note{Reason: "this selection forks and the rewrite conflicts", Ways: ways}
		plan.Blocked = plan.Repair.Sentence()
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
func (s Service) blockedReason(discovery graph.Discovery) repair.Note {
	for _, branch := range discovery.Branches {
		state := discovery.States[branch]
		if state == graph.StateUntracked {
			// The root of a path is the base, not something to rewrite.
			continue
		}
		if state == graph.StateBranchMissing {
			// An edge left behind by a branch deleted with plain Git. There is
			// nothing to retrack -- no branch to record a parent for -- so the
			// way out is to forget the edge.
			return repair.Note{
				Reason: fmt.Sprintf("%s is recorded but is no longer a local branch", branch),
				Ways: []repair.Step{{
					Command: "g2g untrack --branch " + branch,
					Effect:  "forget the edge it left behind",
				}},
			}
		}
		if !state.Restackable() {
			return repair.Note{Reason: fmt.Sprintf("%s is %s · retrack it before restacking", branch, state)}
		}
		// An edge written before fork points were recorded says where the
		// branch hangs but not where its own commits begin. Standing in the
		// parent's tip for that is right only while the branch still sits on
		// it: once the parent moves, the substitute range is empty and the
		// rewrite silently becomes a no-op. Refuse and say so, rather than
		// report success having replayed nothing.
		if edge, tracked := discovery.Graph.Edges[branch]; tracked && edge.ForkPoint == "" && state != graph.StateAligned {
			return repair.Note{Reason: fmt.Sprintf("%s was recorded before fork points were · retrack it before restacking", branch)}
		}
	}
	return repair.Note{}
}

// selectionRoots names the branches the selection records an edge for whose
// parent it does not, which are the ones an --onto or a caller's location
// moves.
//
// It is a property of the selection, not of the store. Asking the store
// instead — whether the recorded parent is tracked anywhere — reparented a
// branch only when its parent happened to be a trunk, because trunks are the
// one thing with no edge. Selected as a subtree, the root's parent is an
// ordinary tracked branch, so --onto was read as "keep the recorded parent",
// the branch sat where it already was, no step was produced and Apply returned
// having done nothing at all.
//
// Trunks carry no edge and are never roots, which is why a path selection
// reparents the branch above the trunk rather than the trunk itself. A trunk
// selection has one root per stack on it, and taking only the first of them
// left every other stack measured against a trunk that was about to move.
func selectionRoots(discovery graph.Discovery) []string {
	roots := make([]string, 0, 1)
	for _, branch := range discovery.Branches {
		edge, tracked := discovery.Graph.Edges[branch]
		if !tracked {
			continue
		}
		if discovery.Graph.Tracked(edge.Parent) && slices.Contains(discovery.Branches, edge.Parent) {
			continue
		}
		roots = append(roots, branch)
	}
	return roots
}

// ontoOneRoot is the way out of an --onto that names several roots: one
// command per root, each moving that root and what is stacked on it.
func ontoOneRoot(roots []string, parent string) repair.Note {
	ways := make([]repair.Step, 0, len(roots))
	for _, root := range roots {
		ways = append(ways, repair.Step{
			Command: fmt.Sprintf("g2g restack --branch %s --onto %s", root, parent),
			Effect:  "move " + root + " and what is stacked on it",
		})
	}
	return repair.Note{
		Reason: fmt.Sprintf("--onto moves one branch and what is stacked on it, and this selection has %d roots: %s", len(roots), strings.Join(roots, ", ")),
		Ways:   ways,
	}
}

// steps builds the ordered rewrite, parents before children so each child is
// measured against the base its parent will actually have.
func (s Service) steps(ctx context.Context, discovery graph.Discovery, onto string, pending Pending) ([]Step, error) {
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
		if onto != "" && slices.Contains(roots, branch) {
			// Only the selection's roots move; everything above them keeps the
			// structure that is already recorded.
			parent = onto
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
			Command: fmt.Sprintf("g2g track --branch %s --parent %s", u.branch, u.parent),
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

// preview asks the replay engine what the rewrite would produce, without
// producing it. A repository whose Git cannot replay gets no prediction, which
// costs the conflict warning but nothing else.
func (s Service) preview(ctx context.Context, plan Plan) (updates []localgit.RefUpdate, clean bool, unpredicted string, err error) {
	supported, err := s.Git.SupportsReplay(ctx)
	if err != nil {
		return nil, false, "", err
	}
	if !supported {
		return nil, false, "this Git cannot preview the result", nil
	}
	// Every step collapsing leaves no group at all: each branch's work is
	// already in its new base by content, so their refs move and nothing is
	// replayed. There is nothing to predict and nothing that could conflict,
	// and asking the engine anyway failed the whole command with "no commit
	// ranges selected for replay" — on precisely the case where a stack has
	// finished landing.
	//
	// A root behind its parent lands on the parent's result, which only the
	// parent's own preview knows. It prints the object for a branch it names;
	// a parent the caller is replacing is named by object instead and prints
	// nothing, and then there is no honest prediction to make.
	clean = true
	replayedTo := map[string]string{}
	for _, group := range plan.groups() {
		onto := group.onto()
		if group[0].Behind {
			tip, known := replayedTo[group[0].Parent]
			if !known {
				return nil, false, group[0].Branch + " lands on " + group[0].Parent + " as it will be once brought down, which cannot be previewed", nil
			}
			onto = tip
		}
		grouped, groupClean, err := s.Git.PreviewReplay(ctx, onto, group.previewed())
		if err != nil {
			return nil, false, "", err
		}
		if !groupClean {
			// A conflict anywhere sends the whole rewrite to the resumable
			// engine, so what the groups above it would do is moot.
			return nil, false, "", nil
		}
		updates = append(updates, grouped...)
		for _, update := range grouped {
			replayedTo[update.Branch()] = update.New
		}
	}
	// Predicted means the preview actually ran, which the two returns above
	// already answer for the cases where it did not. Deriving it from the error
	// being returned alongside read as though a caller might see both, when the
	// only caller bails on the error first.
	return updates, clean, "", nil
}

// moving is every branch whose ref will have moved by the time the rewrite is
// done.
//
// A caller's own moves count as much as the rewrite's: sync collects branches
// before replaying, and a worktree standing on one of those is stranded exactly
// as it would be by a replay. Pending is how a caller says so. Absorbing
// re-records fork points and moves no ref at all, and a collapse onto the
// commit a branch already points at moves nothing either.
func moving(steps []Step, absorb bool, pending Pending) []string {
	branches := slices.Sorted(maps.Keys(pending))
	if absorb {
		return branches
	}
	for _, step := range steps {
		if step.Collapses && step.Head == step.Base {
			continue
		}
		if !slices.Contains(branches, step.Branch) {
			branches = append(branches, step.Branch)
		}
	}
	return branches
}

// HeldElsewhere refuses to move a branch another worktree has checked out.
//
// A rewrite moves a ref without checking anything out, so nothing stopped it
// from moving a branch another worktree held. Git updated the ref; that
// worktree's index and working tree still described the old commit, so its next
// git status reported staged changes nobody made. The preview said "applies
// without touching your working tree or checked-out branch" while doing it,
// which was true of the worktree it ran in and false of the other.
//
// It is asked only of branches that will move. Asking it of the whole
// selection refused a path restack because the trunk, which it never touches,
// was checked out in another worktree. It is exported for a caller that moves
// a ref the plan does not: sync advances the trunk itself.
//
// A Git too old to list worktrees, or a failure to ask, is not a reason to
// refuse a rewrite that was fine before this check existed.
// It answers with structure rather than a sentence. A refusal here reaches a
// caller through sync and land as well as restack, and a machine reading the
// documented contract -- read repair, do not parse the prose -- was handed a
// null where the only two ways out were, on the one refusal a large checkout
// meets first.
//
// The ways out name no command, because the same refusal reaches commands that
// accept different scopes. It used to suggest g2g restack --scope path, which
// was the command that had just refused, and which sync does not accept.
func (s Service) HeldElsewhere(ctx context.Context, branches []string) (repair.Note, error) {
	if len(branches) == 0 {
		return repair.Note{}, nil
	}
	holder, ok := s.Git.(WorktreeReader)
	if !ok {
		return repair.Note{}, nil
	}
	elsewhere, err := holder.CheckedOutElsewhere(ctx)
	if err != nil || len(elsewhere) == 0 {
		return repair.Note{}, nil
	}
	held := make([]string, 0, len(branches))
	for _, branch := range branches {
		if path, taken := elsewhere[branch]; taken {
			held = append(held, fmt.Sprintf("%s (%s)", branch, path))
		}
	}
	if len(held) == 0 {
		return repair.Note{}, nil
	}
	return repair.Note{
		Reason: fmt.Sprintf("checked out in another worktree: %s · moving it would leave that worktree describing a commit it no longer has", strings.Join(held, ", ")),
		Ways: []repair.Step{
			{Effect: "switch that worktree to another branch, or close it"},
			{Effect: "select less with --branch or --scope, so nothing that has to move is checked out there"},
		},
	}, nil
}

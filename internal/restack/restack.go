// Package restack rewrites a stack's contents so they match its recorded
// structure.
//
// It is g2g's only resumable operation. Everything else is one-shot:
// preview, apply, done. A rewrite can stop half-way on a conflict that only a
// person can resolve, so it leaves a record behind and every other command has
// to notice.
//
// The package is split by what the code is deciding. plan.go works out what
// would be replayed and onto what, and touches nothing; this file performs it
// and carries the resume verbs; journal.go is what survives between them.
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
)

// Revalidate re-reads the world and refuses if anything moved since preview.
func (s Service) Revalidate(ctx context.Context, selection graph.Selection, onto Onto, absorb bool, pending Pending, preview Plan) (Plan, error) {
	if err := s.Git.Clean(ctx); err != nil {
		return Plan{}, err
	}
	plan, err := s.Plan(ctx, selection, onto, absorb, pending)
	if err != nil {
		return Plan{}, err
	}
	return plan, diagnostic.Revalidated(ctx, "restack", "restack plan", plan.Equal(preview))
}

// Apply performs the rewrite. A plan the preview said applies cleanly is
// replayed without touching the checkout; anything else takes the resumable
// engine, which needs the user's working tree and says so first.
func (s Service) Apply(ctx context.Context, plan Plan) error {
	if plan.Blocked != "" {
		return fmt.Errorf("cannot restack: %s", plan.Blocked)
	}
	if len(plan.Steps) == 0 {
		// A rewrite with nothing to replay can still have an edge to record: a
		// branch already sitting on the --onto target has no commits to move
		// and a recorded parent that still names where it used to be.
		if moves := plan.reparenting(); len(moves) != 0 {
			return s.recordStructure(ctx, plan.Discovery.Branches, moves)
		}
		return nil
	}
	if plan.Absorb {
		return s.absorb(ctx, plan)
	}
	// Where the checkout stands before anything moves. A rewrite that moves the
	// branch you are on leaves the index and working tree describing the old
	// commit, so the two ends have to be known to reconcile them.
	standing, err := s.standingOn(ctx)
	if err != nil {
		return err
	}
	if plan.inPlace() {
		return s.applyInPlace(ctx, plan, standing)
	}
	if err := s.collapse(ctx, plan); err != nil {
		return err
	}
	return s.rebase(ctx, plan)
}

// applyInPlace journals a rewrite that needs no working tree before it moves
// anything, and forgets it once the rewrite is done or has been put back.
//
// The journal is what a failure that cannot be put back, or a process that
// dies between two replays, leaves for --abort. Every other command refuses
// while it exists, which is right while branches may have moved and wrong once
// they are back where they were.
func (s Service) applyInPlace(ctx context.Context, plan Plan, standing checkout) error {
	if err := s.Journal.Save(ctx, s.record(plan)); err != nil {
		return err
	}
	if err := s.rewriteInPlace(ctx, plan, standing); err != nil {
		var back putBack
		if errors.As(err, &back) {
			if clearErr := s.Journal.Clear(ctx); clearErr != nil {
				return errors.Join(err, clearErr)
			}
		}
		return err
	}
	return s.Journal.Clear(ctx)
}

// record is what survives an interrupted rewrite.
func (s Service) record(plan Plan) Record {
	record := Record{
		OntoParent: plan.Onto.Parent,
		Absorb:     plan.Absorb,
		Branch:     plan.Target,
		Scope:      string(plan.Scope),
		ReturnTo:   plan.Target,
		Original:   map[string]string{},
		Reparent:   plan.reparenting(),
	}
	for _, step := range plan.Steps {
		record.Original[step.Branch] = step.Tip
	}
	return record
}

// standingOn records the branch the checkout is on and where it points.
//
// A detached HEAD has no branch for a rewrite to move underneath it, and an
// unreadable one is not worth failing a rewrite for, so both answer with an
// empty branch and nothing to reconcile later.
func (s Service) standingOn(ctx context.Context) (checkout, error) {
	branch, err := s.Git.CurrentBranch(ctx)
	if err != nil || branch == "" {
		return checkout{}, nil
	}
	tip, err := s.Git.Resolve(ctx, branch)
	if err != nil {
		return checkout{}, nil
	}
	return checkout{Branch: branch, Tip: tip}, nil
}

// checkout is where the working tree stood before a rewrite.
type checkout struct {
	Branch string
	Tip    string
}

// resettle brings the index and working tree to the branch's new tip.
//
// The rebase engine checks out as it goes, so this is only for the paths that
// move a ref without one: the replay engine, and a collapse, which is a bare
// ref move and could strand the checkout just as easily.
func (s Service) resettle(ctx context.Context, standing checkout) error {
	if standing.Branch == "" {
		return nil
	}
	tip, err := s.Git.Resolve(ctx, standing.Branch)
	if err != nil || tip == standing.Tip {
		return err
	}
	diagnostic.Event(ctx, "restack.resettle",
		diagnostic.Field{Key: "branch", Value: standing.Branch},
		diagnostic.Field{Key: "from", Value: standing.Tip},
		diagnostic.Field{Key: "to", Value: tip},
	)
	return s.Git.SwitchTree(ctx, standing.Tip, tip)
}

// verify checks that the engine did what the plan said, rather than reporting
// success because the command exited zero.
//
// Both engines are external and their behaviour varies by version, so the one
// thing worth asserting is the outcome: every branch that was to be rewritten
// now sits on the base it was aimed at. A rewrite that quietly left a branch
// where it was would otherwise be indistinguishable from one that worked, and
// the stack would look repaired while still being wrong.
func (s Service) verify(ctx context.Context, plan Plan) error {
	for _, step := range plan.rewriting() {
		// Against the parent branch rather than the base recorded in the plan:
		// in a stack the parent is being rewritten too, so its planned tip is
		// exactly the commit it no longer points at.
		built, err := s.Git.IsAncestor(ctx, step.Parent, step.Branch)
		if err != nil {
			return err
		}
		if !built {
			return fmt.Errorf("%s was not replayed onto %s; this Git did not perform the rewrite that was planned", step.Branch, step.Parent)
		}
	}
	return nil
}

// collapse moves the branches whose work is entirely in their new base.
//
// Branches that collapse move first so their children are planned against where
// they land, and neither engine ever sees a commit that is already upstream.
// Apply and finish both have to do this, and finish once did only half of it:
// it drove the engine without collapsing first, so a branch that became
// collapsible while a resume was in flight was never moved, the next pass
// computed an identical plan, and the loop hit its own non-convergence guard
// and failed for a case the design has an answer to.
func (s Service) collapse(ctx context.Context, plan Plan) error {
	for _, step := range plan.collapsing() {
		if step.Tip == step.Base {
			continue
		}
		diagnostic.Event(ctx, "restack.collapse", diagnostic.Field{Key: "branch", Value: step.Branch})
		if err := s.Git.UpdateBranch(ctx, step.Branch, step.Base); err != nil {
			return err
		}
	}
	return nil
}

// absorb keeps the commits a parent dropped by re-recording where the branch
// forks, which needs no rewriting at all: the parent's tip is already an
// ancestor of the branch.
func (s Service) absorb(ctx context.Context, plan Plan) error {
	diagnostic.Event(ctx, "restack.absorb", diagnostic.Field{Key: "branches", Value: strings.Join(plan.Branches(), ",")})
	return s.recordStructure(ctx, plan.Discovery.Branches, plan.reparenting())
}

// inPlace reports a plan that needs no working tree: everything it replays
// the preview said applies cleanly, or it only collapses.
func (p Plan) inPlace() bool {
	return p.Clean || len(p.rewriting()) == 0
}

// rewriteInPlace collapses and replays without touching the checkout until the
// end, and is all or nothing.
//
// Independent roots are separate replays, and the engine's atomicity covers
// one invocation, so a failure part-way would otherwise leave some roots moved
// and others not, reported as "Not applied" over refs that had moved. So it
// notes where every branch it may move points before it starts, and on any
// failure before the checkout is touched puts them back and says so.
func (s Service) rewriteInPlace(ctx context.Context, plan Plan, standing checkout) error {
	before, err := s.tips(ctx, plan)
	if err != nil {
		return err
	}
	if err := s.replay(ctx, plan); err != nil {
		return s.putBack(ctx, before, err)
	}
	// A replay or a collapse moves refs without touching the index, so a user
	// standing on a rewritten branch would otherwise see changes they never
	// made — and be unable to switch away, because git refuses to overwrite
	// them.
	if err := s.resettle(ctx, standing); err != nil {
		return err
	}
	return s.recordStructure(ctx, plan.Discovery.Branches, plan.reparenting())
}

// replay collapses what has nothing left, then replays each independent root
// onto its own base and checks the outcome.
func (s Service) replay(ctx context.Context, plan Plan) error {
	if err := s.collapse(ctx, plan); err != nil {
		return err
	}
	groups := plan.groups()
	if len(groups) == 0 {
		return nil
	}
	diagnostic.Event(ctx, "restack.replay", diagnostic.Field{Key: "branches", Value: strings.Join(plan.Replaying(), ",")})
	for _, group := range groups {
		if err := s.Git.Replay(ctx, group.onto(), group.ranges()); err != nil {
			return err
		}
	}
	return s.verify(ctx, plan)
}

// tips is where every branch a plan may move points now, which is what putting
// them back restores. It is read at apply time rather than taken from the
// plan, because a caller may have moved a branch in between: sync collects
// before it replays, and undoing a failed replay must not undo that too.
func (s Service) tips(ctx context.Context, plan Plan) (map[string]string, error) {
	tips := make(map[string]string, len(plan.Steps))
	for _, step := range plan.Steps {
		tip, err := s.Git.Resolve(ctx, step.Branch)
		if err != nil {
			return nil, err
		}
		tips[step.Branch] = tip
	}
	return tips, nil
}

// putBack is a failed in-place rewrite that restored every branch it had
// moved, so the repository is exactly as it was and saying so is true.
type putBack struct{ cause error }

func (p putBack) Error() string {
	return p.cause.Error() + " · nothing was changed: every branch it had moved was put back"
}

func (p putBack) Unwrap() error { return p.cause }

// putBack restores the tips an in-place rewrite started from. A restore that
// fails is reported as such rather than as a put back, because then some
// branches really have moved.
func (s Service) putBack(ctx context.Context, before map[string]string, cause error) error {
	diagnostic.Event(ctx, "restack.put_back", diagnostic.Field{Key: "branches", Value: strings.Join(slices.Sorted(maps.Keys(before)), ",")})
	for _, branch := range slices.Sorted(maps.Keys(before)) {
		now, err := s.Git.Resolve(ctx, branch)
		if err == nil && now == before[branch] {
			continue
		}
		if err == nil {
			err = s.Git.UpdateBranch(ctx, branch, before[branch])
		}
		if err != nil {
			return fmt.Errorf("%w · putting %s back at %s failed too, so branches may have moved: %v · run g2g restack --abort", cause, branch, before[branch], err)
		}
	}
	return putBack{cause: cause}
}

// rebase runs the resumable engine and journals enough to undo the whole
// operation, which git cannot do because it only restores the invocation it is
// running.
func (s Service) rebase(ctx context.Context, plan Plan) error {
	record := s.record(plan)
	if err := s.Journal.Save(ctx, record); err != nil {
		return err
	}
	diagnostic.Event(ctx, "restack.rebase", diagnostic.Field{Key: "branches", Value: strings.Join(plan.Branches(), ",")})
	if err := s.rebaseEach(ctx, plan); err != nil {
		return err
	}
	return s.finish(ctx, record)
}

// rebaseEach replays one branch at a time, bottom-up.
//
// The engines model the work differently and are given it differently. Replay
// takes the whole set at once and needs one shared origin. Rebase moves a
// single line of descent, so each branch is rebased onto the parent it now
// has, re-resolved after that parent has itself moved. Handing rebase the
// whole chain and asking --update-refs to carry the intermediate branches
// works on some versions and not others, and buys nothing that sequencing
// does not.
//
// Stopping part-way is the expected outcome, not a failure: the journal is
// already written and --continue re-derives what is left.
func (s Service) rebaseEach(ctx context.Context, plan Plan) error {
	for _, step := range plan.rewriting() {
		base, err := s.Git.Resolve(ctx, step.Parent)
		if err != nil {
			return err
		}
		if err := s.Git.Rebase(ctx, base, localgit.Range{From: step.ForkPoint, To: step.Branch}); err != nil {
			return err
		}
	}
	return nil
}

// recordStructure writes back what the rewrite actually produced: any branch
// it reparented, and where every branch now forks.
//
// Reparenting has to be recorded here rather than by the caller. A rewrite
// with --onto moves a fragment onto a different base, and leaving the recorded
// parent naming the old one would make the graph describe a structure that no
// longer exists — which every later command, including the next restack, would
// then measure against.
//
// It walks the whole selection rather than the plan's steps. Once a rewrite
// succeeds the re-derived plan has no steps left, so recording only those
// would record nothing at all and leave every fork point describing the world
// before the rewrite.
//
// A branch that is not actually built on its parent is skipped: writing its
// parent's tip as a fork point would assert a range that does not exist.
func (s Service) recordStructure(ctx context.Context, branches []string, reparent map[string]string) error {
	adopted, err := s.Graph.Store.Load(ctx)
	if err != nil {
		return err
	}
	updated, err := reparentStructure(adopted, reparent)
	if err != nil {
		return err
	}
	if err := s.refreshForkPoints(ctx, updated, branches); err != nil {
		return err
	}
	return s.Graph.Store.Save(ctx, updated)
}

// reparentStructure updates only the declared parent relationships. Keeping it
// separate from fork-point refresh makes the two records a restack repairs
// independently visible at their natural seams.
func reparentStructure(adopted graph.Graph, reparent map[string]string) (graph.Graph, error) {
	updated := adopted.Clone()
	for _, branch := range slices.Sorted(maps.Keys(reparent)) {
		parent := reparent[branch]
		edge, tracked := updated.Edges[branch]
		if !tracked || edge.Parent == parent {
			continue
		}
		edge.Parent = parent
		// Adopt keeps the trunk set true on both sides: a new base that nothing
		// else hangs from becomes a root, and a branch that has just gained one
		// stops being one.
		adoptedGraph, _, err := updated.Adopt(branch, edge)
		if err != nil {
			return graph.Graph{}, err
		}
		updated = adoptedGraph
	}
	return updated, nil
}

// refreshForkPoints records the parent tips that describe each replay range.
func (s Service) refreshForkPoints(ctx context.Context, updated graph.Graph, branches []string) error {
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
	return nil
}

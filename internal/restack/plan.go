// This file decides what a restack would do. Nothing in it changes the
// repository: every function here reads, measures, and reports.
package restack

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/graph"
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
		return plan.refused(blocked), nil
	}
	if roots := selectionRoots(discovery); onto.Reparents() && len(roots) > 1 {
		// --onto moves one branch and what is stacked on it. Moving only the
		// first of several roots is what it used to do, silently.
		return plan.refused(ontoOneRoot(roots, onto.Parent)), nil
	}
	steps, err := s.steps(ctx, discovery, onto, pending)
	var unmeasurable unmeasured
	if errors.As(err, &unmeasurable) {
		return plan.refused(unmeasurable.note()), nil
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
		plan.Held = true
		return plan.refused(held), nil
	}
	plan.Steps = steps
	if len(steps) == 0 {
		return plan, nil
	}
	if plan.Subjects, err = s.subjects(ctx, plan.Orphaned()); err != nil {
		return Plan{}, err
	}
	if plan.Absorb && !plan.Absorbable() {
		return plan.refused(repair.Note{Reason: "commits the parent dropped were rewritten rather than removed, so absorbing them would duplicate work the parent still carries"}), nil
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
		plan = plan.refused(forkConflict(plan.Lines))
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
				Reason: noLongerLocal([]string{branch}),
				Ways: []repair.Step{{
					Command: "g2g untrack --branch " + repair.Quote(branch),
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

// noLongerLocal is why branches the graph records cannot be restacked: they
// were deleted or renamed with plain Git.
func noLongerLocal(branches []string) string {
	if len(branches) == 1 {
		return branches[0] + " is recorded but is no longer a local branch"
	}
	return strings.Join(branches, ", ") + " are recorded but are no longer local branches"
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

// refused is this plan refused for the reason note gives, with the sentence a
// machine reads derived from the note a person reads, as every sibling
// planner's is.
func (p Plan) refused(note repair.Note) Plan {
	p.Repair = note
	return p
}

// forkConflict is the way out of a forked selection whose rewrite conflicts:
// one line of descent at a time, ending at each leaf.
func forkConflict(leaves []string) repair.Note {
	ways := make([]repair.Step, 0, len(leaves))
	for _, leaf := range leaves {
		ways = append(ways, repair.Step{Command: "g2g restack --branch " + repair.Quote(leaf) + " --scope path", Effect: "rewrite the line of descent ending at " + leaf})
	}
	return repair.Note{Reason: "this selection forks and the rewrite conflicts", Ways: ways}
}

// ontoOneRoot is the way out of an --onto that names several roots: one
// command per root, each moving that root and what is stacked on it.
func ontoOneRoot(roots []string, parent string) repair.Note {
	ways := make([]repair.Step, 0, len(roots))
	for _, root := range roots {
		ways = append(ways, repair.Step{
			Command: fmt.Sprintf("g2g restack --branch %s --onto %s", repair.Quote(root), repair.Quote(parent)),
			Effect:  "move " + root + " and what is stacked on it",
		})
	}
	return repair.Note{
		Reason: fmt.Sprintf("--onto moves one branch and what is stacked on it, and this selection has %d roots: %s", len(roots), strings.Join(roots, ", ")),
		Ways:   ways,
	}
}

// subjects names the commits a preview is about to list, when Git can say.
func (s Service) subjects(ctx context.Context, ids []string) (map[string]string, error) {
	describer, ok := s.Git.(Describer)
	if !ok || len(ids) == 0 {
		return nil, nil
	}
	commits, err := describer.Describe(ctx, ids)
	if err != nil {
		return nil, err
	}
	named := make(map[string]string, len(commits))
	for _, commit := range commits {
		named[commit.ID] = commit.Subject
	}
	return named, nil
}

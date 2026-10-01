package land

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/stack"
	syncer "github.com/shhac/g2g/internal/sync"
)

// blockedBefore is everything that refuses the whole descent before any of it
// is decided branch by branch.
//
// Each of these is somebody else's refusal, asked here so it arrives before the
// first merge rather than after it. A diverged trunk discovered half way down
// leaves a stack that has partly landed and cannot be replayed.
//
// It answers with a sentence as well as the structure behind it, because a
// refusal that reaches a plan from another one may carry only the sentence:
// sync sets Blocked straight from the restack it delegates to, with no Repair
// beside it. Reading the structure alone let exactly that refusal through.
func (s Service) blockedBefore(ctx context.Context, plan *Plan, recorded graph.Graph) (string, repair.Note) {
	if sentence, note := structuralRefusal(*plan, recorded); sentence != "" {
		return sentence, note
	}
	discovery, options := plan.Discovery, plan.Options
	if dirty := s.dirtyWhereItMatters(ctx, recorded, discovery.Target, discovery.Base); dirty.Reason != "" {
		return dirty.Sentence(), dirty
	}
	if held := s.heldElsewhere(ctx, recorded, plan); held.Reason != "" {
		return held.Sentence(), held
	}
	// An error planning the push is not a refusal here: each cycle plans its
	// own publish again before its merge, so the same error stops the descent
	// before anything has merged.
	pushed, err := s.Pusher.Plan(ctx, pushSelection(*plan), options.Remote, options.Upstream)
	if err == nil && pushed.Blocked != "" {
		return pushed.Blocked, pushed.Repair
	}
	if plan.KeepTrunk {
		return "", repair.Note{}
	}
	synced, err := s.Syncer.Plan(ctx, syncSelection(*plan, discovery.Target), options.Remote, syncer.TakeNothing)
	if err != nil && plan.declared() {
		// Nothing about a base alone makes sync unable to answer, so a failure
		// here is one the advance after the merge would meet too.
		return err.Error(), repair.Note{}
	}
	if err == nil && synced.Blocked != "" {
		return synced.Blocked, synced.Repair
	}
	return "", repair.Note{}
}

// structuralRefusal is every refusal the plan's own shape answers, with
// nothing asked of Git or the remote.
func structuralRefusal(plan Plan, recorded graph.Graph) (string, repair.Note) {
	discovery := plan.Discovery
	if err := discovery.RequireLinear("land"); err != nil {
		return err.Error(), repair.Note{}
	}
	if err := discovery.RequireActionable("g2g land"); err != nil {
		return err.Error(), repair.Note{}
	}
	if len(discovery.Branches) == 0 {
		return "nothing is stacked here to land", repair.Note{}
	}
	// Landing reads pull requests from whichever source describes the stack and
	// then replays, reparents and forgets in g2g's own graph. Those are not the
	// same record. Told to act on a structure g2g has not adopted, it would
	// merge every pull request and then find nothing to replay and nothing to
	// forget -- a stack taken apart on GitHub and left untouched here.
	if discovery.Source != stack.SourceG2G {
		note := repair.Note{
			Reason: fmt.Sprintf("this stack is described by %s, and landing rewrites the branches above each merge in g2g's own graph", discovery.Source),
			Ways: []repair.Step{
				{Command: "g2g adopt", Effect: "adopt it, so there is a structure to replay against"},
			},
		}
		return note.Sentence(), note
	}
	if discovery.Target == discovery.Base {
		note := repair.Note{
			Reason: fmt.Sprintf("%s is a trunk, and landing it would merge every branch above it", discovery.Target),
			Ways:   []repair.Step{{Effect: "stand on the branch you mean to land, or name it with --branch"}},
		}
		return note.Sentence(), note
	}
	if plan.declared() {
		if note := declaredRefusal(recorded, discovery.Target); note.Reason != "" {
			return note.Sentence(), note
		}
	}
	if note := linkedOnGitHub(discovery); note.Reason != "" {
		return note.Sentence(), note
	}
	return "", repair.Note{}
}

// linkedOnGitHub refuses a descent through a pull request in a GitHub native
// stack.
//
// GitHub refuses to merge one of those through gh pr merge, and the endpoint it
// asks for instead merges everything below it in the stack at once, which is
// not a descent: nothing above would be replayed between merges. Unlinking
// leaves the pull requests as they are, so it is the way out rather than
// something land does on its own; were GitHub to accept the ordinary merge,
// this gate is all there is to remove.
func linkedOnGitHub(discovery stack.Discovery) repair.Note {
	resolutions := githubstack.ResolveHeads(discovery.PullRequests)
	var numbers []string
	var stacks []int
	for _, branch := range discovery.Branches {
		open := resolutions[branch].Open
		if open == nil || open.StackNumber == 0 {
			continue
		}
		numbers = append(numbers, fmt.Sprintf("#%d", open.Number))
		if !slices.Contains(stacks, open.StackNumber) {
			stacks = append(stacks, open.StackNumber)
		}
	}
	if len(stacks) == 0 {
		return repair.Note{}
	}
	ways := make([]repair.Step, 0, len(stacks))
	for _, number := range stacks {
		ways = append(ways, repair.Step{
			Command: fmt.Sprintf("g2g github unlink --branch %s --stack-number %d", discovery.Target, number),
			Effect:  "unlink the GitHub stack, keeping its pull requests",
		})
	}
	return repair.Note{
		Reason: fmt.Sprintf("%s %s in a GitHub stack, and GitHub will not merge a stacked pull request on its own", strings.Join(numbers, ", "), pick(len(numbers), "is", "are")),
		Ways:   ways,
	}
}

// heldElsewhere refuses a descent that would move a branch another worktree
// has checked out: the trunk it advances, the branches it merges and deletes,
// and those above it that it replays.
//
// Each step's own sync refuses the same thing, but only once there is
// something to move — after the first merge, which does not come back. Asking
// sync up front found nothing while the trunk was level, so a descent with the
// trunk open in another worktree merged its bottom branch and then stopped.
func (s Service) heldElsewhere(ctx context.Context, recorded graph.Graph, plan *Plan) repair.Note {
	if s.Holds == nil {
		return repair.Note{}
	}
	cannotTell := func(err error) repair.Note {
		return repair.Note{Reason: "cannot tell whether another worktree has a branch this would move: " + err.Error()}
	}
	target, base := plan.Target, plan.Trunk
	branches, err := moving(recorded, target, base)
	if err != nil {
		return cannotTell(err)
	}
	held, err := s.Holds.HeldElsewhere(ctx, branches)
	if err != nil {
		return cannotTell(err)
	}
	if held.Reason == "" {
		return repair.Note{}
	}
	// A single branch can merge without advancing the trunk here, provided
	// there is no survivor whose contents would need replaying afterwards.
	if len(plan.Branches) == 1 && len(recorded.Children(target)) == 0 {
		others := slices.DeleteFunc(slices.Clone(branches), func(branch string) bool { return branch == base })
		note, err := s.Holds.HeldElsewhere(ctx, others)
		if err != nil {
			return cannotTell(err)
		}
		if note.Reason == "" {
			current, err := s.Git.CurrentBranch(ctx)
			if err != nil {
				return cannotTell(err)
			}
			plan.KeepTrunk = true
			plan.Detach = plan.Options.DeleteLocal && current == target
			return repair.Note{}
		}
	}
	// Narrowing the selection is no way out here: a descent moves the whole
	// stack whatever was selected, because the replay after each merge takes
	// everything above it.
	return repair.Note{Reason: held.Reason, Ways: []repair.Step{{Effect: "switch that worktree to another branch, or close it"}}}
}

// dirtyWhereItMatters refuses uncommitted work only where the descent would
// touch it.
//
// Most of a descent never goes near the checkout: the merge happens on GitHub,
// a replay that applies cleanly moves refs without it, and deleting a branch
// that is not checked out needs nothing from it. Refusing every dirty tree
// stopped someone landing a lone branch while another held work in progress,
// and stashing it is no answer when the stash is shared with other worktrees.
// What does touch it is moving the branch checked out here, and a replay that
// conflicts, which is resolved in this working tree -- found out before each
// replay rather than guessed here.
func (s Service) dirtyWhereItMatters(ctx context.Context, recorded graph.Graph, target, base string) repair.Note {
	why := s.touchesCheckout(ctx, recorded, target, base)
	if why == "" {
		return repair.Note{}
	}
	if err := s.Git.Clean(ctx); err != nil {
		return repair.Note{Reason: why + " · " + err.Error(), Ways: []repair.Step{{Effect: "commit the changes, or land from a checkout that has none"}}}
	}
	return repair.Note{}
}

// touchesCheckout says why the descent needs this working tree, empty when it
// does not.
func (s Service) touchesCheckout(ctx context.Context, recorded graph.Graph, target, base string) string {
	branches, err := moving(recorded, target, base)
	if err != nil {
		return "which branches landing moves could not be worked out"
	}
	current, err := s.Git.CurrentBranch(ctx)
	if err != nil {
		return "which branch is checked out here could not be read"
	}
	if current != "" && slices.Contains(branches, current) {
		return fmt.Sprintf("%s is checked out here, and landing moves it", current)
	}
	// A replay that conflicts would need the tree too, and is only known once
	// the merge below it has happened; replayableHere asks then.
	return ""
}

// moving is every branch a descent can move: the target's whole stack, and the
// base it advances. A declared trunk is a root of its own, so its stack does
// not reach the base it lands into, which is why the base is added rather than
// assumed.
func moving(recorded graph.Graph, target, base string) ([]string, error) {
	branches, err := recorded.Shape().Stack(target)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(branches, base) {
		branches = append(branches, base)
	}
	return branches, nil
}

package land

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/words"
)

// BaseMove is a pull request the descent points at the trunk because it is
// about to delete, on the remote, the branch that pull request sits on.
//
// GitHub closes a pull request whose base branch is deleted with a push, and
// a closed one can then be neither retargeted nor reopened. Waiting for its
// own turn was too late for the next branch up, and a branch above the
// selection never got a turn at all.
type BaseMove struct {
	Number int
	ID     string
	Head   string
	From   string
	Base   string
}

// planMoves decides, once, every pull request the descent moves before a
// deletion: each later step still sitting on a branch landed before it, then
// each survivor outside the selection on a branch the descent deletes. A step
// keeps its From, which is a fact about where it points now and what the
// preview marks; whether its own merge still moves it is retargetsAtMerge.
func planMoves(plan Plan, found survivors) []BaseMove {
	if !plan.Options.DeleteRemote {
		return nil
	}
	landing := make(map[string]bool, len(plan.Steps))
	var moves []BaseMove
	for _, step := range plan.Steps {
		if step.Retargets() && landing[step.From] {
			moves = append(moves, BaseMove{Number: step.Number, ID: step.ID, Head: step.Branch, From: step.From, Base: step.Base})
		}
		landing[step.Branch] = true
	}
	resolutions := githubstack.ResolveHeads(found.prs)
	for _, branch := range found.affected {
		open := resolutions[branch].Open
		if open == nil || !landing[open.Base] || landing[branch] {
			continue
		}
		moves = append(moves, BaseMove{Number: open.Number, ID: open.ID, Head: branch, From: open.Base, Base: plan.Trunk})
	}
	return moves
}

// retargetsAtMerge reports a step whose own merge has to move its pull
// request: one the cleanup below it does not move first. Apply, the queue
// path and the recipe all ask this, so they cannot come to disagree.
func (p Plan) retargetsAtMerge(step Step) bool {
	return step.Retargets() && !slices.ContainsFunc(p.Moves, func(move BaseMove) bool { return move.Number == step.Number })
}

// moves are the pull requests to point at the trunk before step's branch is
// deleted on the remote.
func (p Plan) moves(step Step) []BaseMove {
	var moves []BaseMove
	for _, move := range p.Moves {
		if move.From == step.Branch {
			moves = append(moves, move)
		}
	}
	return moves
}

// rehome points the pull requests on step's branch at the trunk, in one
// request, before the branch is deleted. A move GitHub refuses stops the
// descent with the branch kept: deleting it would close that pull request.
func (s Service) rehome(ctx context.Context, plan Plan, step Step, changed *[]string) error {
	moves := plan.moves(step)
	if len(moves) == 0 {
		return nil
	}
	changes := make([]githubstack.BaseChange, 0, len(moves))
	for _, move := range moves {
		diagnostic.Event(ctx, "land.rehome",
			diagnostic.Field{Key: "number", Value: fmt.Sprint(move.Number)},
			diagnostic.Field{Key: "from", Value: move.From},
			diagnostic.Field{Key: "to", Value: move.Base},
		)
		changes = append(changes, githubstack.BaseChange{ID: move.ID, Number: move.Number, Base: move.Base})
	}
	outcomes, err := s.GitHub.Retarget(ctx, changes)
	if err != nil {
		return kept(plan, step, moves, err)
	}
	var refused []BaseMove
	var first error
	for index, outcome := range outcomes {
		if outcome == nil {
			*changed = append(*changed, fmt.Sprintf("pointed #%d at %s", moves[index].Number, moves[index].Base))
			continue
		}
		refused = append(refused, moves[index])
		first = cmp.Or(first, outcome)
	}
	if len(refused) != 0 {
		return kept(plan, step, refused, first)
	}
	return nil
}

// kept is why a landed branch was left on the remote: the pull requests on it
// could not be moved. It is still recorded, so the way to finish is to move
// each one by hand and land again, which tidies it. The move is named exactly,
// as the recipe names it: github retarget would aim each at the branch it is
// recorded on, which is this one.
func kept(plan Plan, step Step, moves []BaseMove, err error) error {
	numbers := make([]string, 0, len(moves))
	edits := make([]string, 0, len(moves))
	for _, move := range moves {
		numbers = append(numbers, fmt.Sprintf("#%d", move.Number))
		edits = append(edits, repair.Command([]string{"gh", "pr", "edit", fmt.Sprint(move.Number), "--base", move.Base}))
	}
	return fmt.Errorf("could not point %s at %s (%v), so %s is kept on %s rather than closing %s · run %s, then %s",
		words.List(numbers), plan.Trunk, err, step.Branch, plan.Options.Remote, words.Pick(len(moves), "it", "them"),
		words.List(edits), plan.RetryCommand()+" --apply")
}

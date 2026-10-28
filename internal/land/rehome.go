package land

import (
	"cmp"
	"context"
	"fmt"

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

// survivorMoves are the open pull requests outside the selection that sit on a
// branch the descent deletes on the remote.
func survivorMoves(plan Plan, found survivors) []BaseMove {
	if !plan.Options.DeleteRemote {
		return nil
	}
	deleted := make(map[string]bool, len(plan.Steps))
	for _, step := range plan.Steps {
		deleted[step.Branch] = true
	}
	resolutions := githubstack.ResolveHeads(found.prs)
	var moves []BaseMove
	for _, branch := range found.affected {
		open := resolutions[branch].Open
		if open == nil || !deleted[open.Base] || plan.landing(branch) {
			continue
		}
		moves = append(moves, BaseMove{Number: open.Number, ID: open.ID, Head: branch, From: open.Base, Base: plan.Trunk})
	}
	return moves
}

// landing reports a branch the descent has a step for.
func (p Plan) landing(branch string) bool {
	for _, step := range p.Steps {
		if step.Branch == branch {
			return true
		}
	}
	return false
}

// movedEarly reports a step whose pull request sits on a branch landed before
// it, which the cleanup of that branch points at the trunk before deleting it,
// so the step's own merge has nothing left to move.
func (p Plan) movedEarly(step Step) bool {
	if !p.Options.DeleteRemote || !step.Retargets() {
		return false
	}
	for _, below := range p.Steps {
		if below.Branch == step.Branch {
			return false
		}
		if below.Branch == step.From {
			return true
		}
	}
	return false
}

// moves are the pull requests to point at the trunk before step's branch is
// deleted on the remote: a later step still sitting on it, and any survivor
// outside the selection.
func (p Plan) moves(step Step) []BaseMove {
	var moves []BaseMove
	for _, above := range p.Steps {
		if above.From == step.Branch && p.movedEarly(above) {
			moves = append(moves, BaseMove{Number: above.Number, ID: above.ID, Head: above.Branch, From: above.From, Base: above.Base})
		}
	}
	for _, survivor := range p.Survivors {
		if survivor.From == step.Branch {
			moves = append(moves, survivor)
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

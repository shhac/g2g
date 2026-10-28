package land

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/repair"
)

// survivors are the branches a descent's replay can move, with their pull
// requests: the recorded subtree from the first branch of the selection the
// graph records, and what lies outside the selection read in one request.
type survivors struct {
	root     string
	affected []string
	prs      []githubstack.PullRequest
}

// readSurvivors reads the survivors of plan. Branches outside the selection
// are the only ones it asks GitHub about; the selection's own pull requests
// came with the plan.
func (s Service) readSurvivors(ctx context.Context, plan Plan, recorded graph.Graph) (survivors, error) {
	found := survivors{prs: plan.PullRequests}
	for _, branch := range plan.Branches {
		if recorded.Records(branch) {
			found.root = branch
			break
		}
	}
	if found.root == "" {
		return found, nil
	}
	found.affected = recorded.Shape().Subtree(found.root)
	var extras []string
	for _, branch := range found.affected {
		if !slices.Contains(plan.Branches, branch) {
			extras = append(extras, branch)
		}
	}
	if len(extras) == 0 {
		return found, nil
	}
	more, err := s.GitHub.Inspect(ctx, extras)
	if err != nil {
		return survivors{}, err
	}
	found.prs = slices.Clone(found.prs)
	known := make(map[int]bool, len(found.prs))
	for _, pr := range found.prs {
		known[pr.Number] = true
	}
	for _, pr := range more {
		if !slices.Contains(extras, pr.Head) || known[pr.Number] {
			continue
		}
		found.prs = append(found.prs, pr)
		known[pr.Number] = true
	}
	return found, nil
}

// pendingSurvivors protects every branch the replay can move, including those
// above a target that selected only the bottom of a stack.
func (s Service) pendingSurvivors(ctx context.Context, plan Plan, found survivors, readiness githubstack.Mergeability, allowPendingRoot bool) (repair.Note, error) {
	if found.root == "" {
		return repair.Note{}, nil
	}
	resolutions := githubstack.ResolveHeads(found.prs)
	var numbers []int
	for _, branch := range found.affected {
		if open := resolutions[branch].Open; open != nil {
			if _, known := readiness.States[open.Number]; known {
				continue
			}
			numbers = append(numbers, open.Number)
		}
	}
	states := make(map[int]githubstack.MergeState, len(readiness.States))
	for number, state := range readiness.States {
		states[number] = state
	}
	if len(numbers) != 0 {
		more, err := s.GitHub.Mergeability(ctx, numbers, plan.Trunk)
		if err != nil {
			return repair.Note{}, err
		}
		for number, state := range more.States {
			states[number] = state
		}
	}
	for _, branch := range found.affected {
		open := resolutions[branch].Open
		if open == nil {
			continue
		}
		state := states[open.Number]
		if state.Pending() && (branch != found.root || plan.Queue == nil || !allowPendingRoot) {
			return repair.Note{Reason: fmt.Sprintf("%s (#%d) has a pending merge request and this descent would change its head or base", branch, open.Number), Ways: []repair.Step{{Effect: "finish or remove that merge request before landing its ancestors"}}}, nil
		}
	}
	return repair.Note{}, nil
}

// guardSurvivors refuses, as an error, a descent that would move a survivor
// with a pending merge request. It reads them afresh: by apply, a request may
// have been made since the plan.
func (s Service) guardSurvivors(ctx context.Context, plan Plan, readiness githubstack.Mergeability, allowPendingRoot bool) error {
	recorded, err := s.Graph.Store.Load(ctx)
	if err != nil {
		return err
	}
	found, err := s.readSurvivors(ctx, plan, recorded)
	if err != nil {
		return err
	}
	note, err := s.pendingSurvivors(ctx, plan, found, readiness, allowPendingRoot)
	if err != nil || note.Reason == "" {
		return err
	}
	return errors.New(note.Sentence())
}

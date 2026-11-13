package submit

import (
	"context"
	"maps"
	"slices"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/stack"
)

// What a submission leaves out: a branch above the one submitted that has
// nothing to submit yet.

// waiting is each branch above the target with nothing to submit, in
// selection order: no open pull request and no commit beyond what it sits on.
// A branch just created to work in next is one, and taking it in asked for a
// title it could not use and a pull request GitHub refuses.
func (s Service) waiting(ctx context.Context, snapshot stack.Snapshot, prs []githubstack.PullRequest) ([]string, error) {
	resolutions := githubstack.ResolveHeads(prs)
	return leftOut(snapshot, func(branch string) (bool, error) {
		// Two open pull requests resolve to none, and leaving that branch
		// out would hide the ambiguity that blocks it.
		if resolutions[branch].OpenCount != 0 {
			return false, nil
		}
		return s.Git.IsAncestor(ctx, branch, snapshot.SitsOn(branch))
	})
}

// leftOut is the branches above the target that idle says have nothing to
// submit and that nothing submitted sits on. The target itself is never left
// out, because naming it is asking, and neither is a branch a submitted one
// sits on, because its pull request is what that one is based on.
//
// Each arm of a fork is asked on its own, from its top down, so an empty
// branch at the top of one arm is left out while the other arm is submitted.
// idle is asked only of a branch everything above has been left out of,
// which is what keeps the questions to Git to the candidates.
func leftOut(snapshot stack.Snapshot, idle func(string) (bool, error)) ([]string, error) {
	if !slices.Contains(snapshot.Branches, snapshot.Target) {
		return nil, nil
	}
	above := snapshot.Shape().Subtree(snapshot.Target)[1:]
	held, left := map[string]bool{}, map[string]bool{}
	// Reversed pre-order reaches every branch before what it sits on.
	for _, branch := range slices.Backward(above) {
		if !held[branch] {
			empty, err := idle(branch)
			if err != nil {
				return nil, err
			}
			if empty {
				left[branch] = true
				continue
			}
		}
		held[snapshot.SitsOn(branch)] = true
	}
	return slices.DeleteFunc(slices.Clone(snapshot.Branches), func(branch string) bool { return !left[branch] }), nil
}

// without is the selection with the branches left out of it removed.
func without(snapshot stack.Snapshot, left []string) stack.Snapshot {
	if len(left) == 0 {
		return snapshot
	}
	parents := maps.Clone(snapshot.Parents)
	for _, branch := range left {
		delete(parents, branch)
	}
	snapshot.Branches = slices.DeleteFunc(slices.Clone(snapshot.Branches), func(branch string) bool { return slices.Contains(left, branch) })
	snapshot.Parents = parents
	return snapshot
}

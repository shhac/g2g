package submit

import (
	"context"
	"fmt"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/repair"
)

// A mismatched base must be retargeted before publishing would make it contain
// the PR's head. GitHub treats that ancestry as a merge, irreversibly closing
// the PR. Correct bases need no retarget. Use local tips, including local bases
// outside the selection, and no additional GitHub reads. Nonlocal bases cannot
// be assessed here.
func (s Service) preservedBaseRepair(ctx context.Context, plan Plan) (repair.Note, error) {
	misbased := make(map[string]*githubstack.PullRequest)
	for _, step := range plan.misbased() {
		misbased[step.Branch] = step.Resolution.Open
	}
	var revisions []string
	for _, lease := range plan.Push.Leases() {
		if pr := misbased[lease.Branch]; pr != nil {
			revisions = append(revisions, "refs/heads/"+lease.Branch, "refs/heads/"+pr.Base)
		}
	}
	if len(revisions) == 0 {
		return repair.Note{}, nil
	}
	tips, err := s.Git.ResolveAll(ctx, revisions)
	if err != nil {
		return repair.Note{}, err
	}
	for _, lease := range plan.Push.Leases() {
		pr := misbased[lease.Branch]
		if pr == nil {
			continue
		}
		head, base := tips["refs/heads/"+lease.Branch], tips["refs/heads/"+pr.Base]
		if head == "" || base == "" {
			continue
		}
		contains, err := s.Git.IsAncestor(ctx, head, base)
		if err != nil {
			return repair.Note{}, err
		}
		if contains {
			return repair.Note{
				Reason: fmt.Sprintf("preserved base %s would contain the pushed head of PR #%d (%s), which GitHub would mark merged", pr.Base, pr.Number, lease.Branch),
				Ways:   []repair.Step{{Command: plan.retarget(), Effect: "align the PR bases before publishing"}},
			}, nil
		}
	}
	return repair.Note{}, nil
}

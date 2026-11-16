package submit

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/repair"
)

// A mismatched base must be retargeted before publication would make it contain
// the head. Pushed bases use their proposed tips; untouched bases use observed
// remote tips. Missing refs or objects provide no ancestry assertion.
func (s Service) preservedBaseRepair(ctx context.Context, plan Plan) (repair.Note, error) {
	misbased := make(map[string]*githubstack.PullRequest)
	for _, step := range plan.misbased() {
		misbased[step.Branch] = step.Resolution.Open
	}
	leases := plan.Push.Leases()
	pushed := make(map[string]bool, len(leases))
	for _, lease := range leases {
		pushed[lease.Branch] = true
	}
	remote := maps.Clone(plan.Push.RemoteTips)
	if remote == nil {
		remote = map[string]string{}
	}
	// push observed every selected branch, including branches it leaves landed.
	observed := make(map[string]bool)
	for branch := range remote {
		observed[branch] = true
	}
	for _, branch := range plan.Push.Branches {
		observed[branch] = true
	}
	missing := make(map[string]bool)
	for _, lease := range leases {
		if pr := misbased[lease.Branch]; pr != nil && !pushed[pr.Base] && !observed[pr.Base] {
			missing[pr.Base] = true
		}
	}
	if len(missing) != 0 {
		tips, err := s.Git.RemoteTips(ctx, plan.Remote, slices.Sorted(maps.Keys(missing)))
		if err != nil {
			return repair.Note{}, err
		}
		maps.Copy(remote, tips)
	}
	bases := make(map[string]string)
	var revisions []string
	for _, lease := range leases {
		if pr := misbased[lease.Branch]; pr != nil {
			base := remote[pr.Base]
			if pushed[pr.Base] {
				base = "refs/heads/" + pr.Base
			}
			bases[lease.Branch] = base
			revisions = append(revisions, "refs/heads/"+lease.Branch)
			if base != "" {
				revisions = append(revisions, base)
			}
		}
	}
	if len(revisions) == 0 {
		return repair.Note{}, nil
	}
	tips, err := s.Git.ResolveAll(ctx, revisions)
	if err != nil {
		return repair.Note{}, err
	}

	for _, lease := range leases {
		pr := misbased[lease.Branch]
		if pr == nil {
			continue
		}
		head, base := tips["refs/heads/"+lease.Branch], tips[bases[lease.Branch]]
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

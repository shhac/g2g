package link

import (
	"context"
	"slices"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/landed"
	"github.com/shhac/g2g/internal/parallel"
)

// Whether a branch's work is already below it, asked of Git rather than of the
// pull request.
//
// status says a branch has landed rather than that it has no pull request,
// because GitHub cannot answer that one: a squash merge lands the work under a
// head the branch never had. Its tests are in landed_real_test.go, for the
// same reason currency's are.

// markLanded re-reads the branches whose only problem is that they have no
// pull request to project, and says so differently where the work is already
// below them.
//
// Only those are asked, which is what bounds the cost: a branch with an open
// pull request has somewhere for its work to be, and one whose pull request
// merged still needs its content checked against the trunk. Only ask
// for the branches where the answer changes what to do.
func (s Service) markLanded(ctx context.Context, plan Plan) error {
	if s.Tips == nil {
		return nil
	}
	trunk := plan.Snapshot.Trunk()
	resolutions := githubstack.ResolveHeads(plan.PullRequests)
	asking := make([]string, len(plan.Issues))
	for index, issue := range plan.Issues {
		if (issue.Kind == IssueMissing || issue.Kind == IssueClosed || issue.Kind == IssueMergedUnlanded) && plan.here(issue.Branch) {
			asking[index] = issue.Branch
		}
	}
	// Distinct elements of a slice that already exists, so the writes need no
	// lock: each read owns the one issue it was given.
	return parallel.Each(ctx, asking, func(ctx context.Context, index int, branch string) error {
		if branch == "" {
			return nil
		}
		if plan.Issues[index].Kind == IssueMergedUnlanded {
			if trunk == "" || slices.Contains(plan.Absent, trunk) {
				return nil
			}
			// The former base may now contain the head. Limiting at that base
			// would exclude the very work we must check; compare the range above
			// the trunk instead, never the commits the trunk gained.
			inTrunk, err := landed.Into(ctx, s.Tips, trunk, branch, trunk)
			if err != nil {
				return nil
			}
			if inTrunk {
				plan.Issues[index].Kind = IssueMerged
				plan.Issues[index].Reason = "PR merged; landed in " + trunk
			} else {
				pr := resolutions[branch].Latest
				plan.Issues[index].Reason = "merged into " + pr.Base + ", not landed"
			}
			return nil
		}
		if !s.landed(ctx, plan, branch) {
			return nil
		}
		below := plan.SitsOn(branch)
		plan.Issues[index] = Issue{
			Branch: branch,
			Kind:   IssueLanded,
			Number: plan.Issues[index].Number,
			Reason: "landed in " + below,
		}
		return nil
	})
}

// landed reports a branch with nothing left to contribute to the branch below
// it. The cheap per-commit question comes first; the whole-branch merge is
// asked only of what it says no to, because that is the squash-merge case and
// it is the more expensive read.
// It cannot fail. A branch sharing no history with the one below it cannot be
// compared, which is an answer rather than a failure, so the only fallible call
// in here is answered rather than returned.
func (s Service) landed(ctx context.Context, plan Plan, branch string) bool {
	below := plan.SitsOn(branch)
	upstream, err := landed.Into(ctx, s.Tips, below, branch, below)
	return err == nil && upstream
}

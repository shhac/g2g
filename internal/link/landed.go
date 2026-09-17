package link

import (
	"context"

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
// merged has already been answered by GitHub. Two local reads each, and only
// for the branches where the answer changes what to do.
func (s Service) markLanded(ctx context.Context, plan Plan) error {
	if s.Tips == nil {
		return nil
	}
	asking := make([]string, len(plan.Issues))
	for index, issue := range plan.Issues {
		if issue.Kind == IssueMissing || issue.Kind == IssueClosed {
			asking[index] = issue.Branch
		}
	}
	// Distinct elements of a slice that already exists, so the writes need no
	// lock: each read owns the one issue it was given.
	return parallel.Each(ctx, asking, func(ctx context.Context, index int, branch string) error {
		if branch == "" {
			return nil
		}
		landed, err := s.landed(ctx, plan, branch)
		if err != nil {
			return err
		}
		if !landed {
			return nil
		}
		below := ownCommitsFrom(plan, branch)
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
func (s Service) landed(ctx context.Context, plan Plan, branch string) (bool, error) {
	below := ownCommitsFrom(plan, branch)
	// A branch sharing no history with the one below it cannot be compared,
	// which is an answer rather than a failure.
	upstream, err := landed.Into(ctx, s.Tips, below, branch, below)
	if err != nil {
		return false, nil
	}
	return upstream, nil
}

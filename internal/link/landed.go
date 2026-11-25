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
	resolutions := githubstack.ResolveHeads(plan.PullRequests)
	parents := make([]string, 0)
	for _, issue := range plan.Issues {
		previous := resolutions[issue.Branch].Latest
		parent := plan.SitsOn(issue.Branch)
		if previous != nil && previous.Merged() && previous.Base == parent && parent != plan.Trunk() && plan.here(issue.Branch) {
			if !slices.Contains(parents, parent) {
				parents = append(parents, parent)
			}
		}
	}
	parentTips := landed.KnownParents(ctx, s.Tips, "origin", parents)
	// Each read owns one preallocated issue slot; policy stays in assessLanded.
	return parallel.Each(ctx, plan.Issues, func(ctx context.Context, index int, issue Issue) error {
		plan.Issues[index] = s.assessLanded(ctx, plan, issue, resolutions[issue.Branch].Latest, parentTips[plan.SitsOn(issue.Branch)])
		return nil
	})
}

// assessLanded assesses one issue independently of scheduling. Unavailable
// evidence leaves its existing classification and reason unchanged.
func (s Service) assessLanded(ctx context.Context, plan Plan, issue Issue, previous *githubstack.PullRequest, knownParent string) Issue {
	if s.Tips == nil || !plan.here(issue.Branch) {
		return issue
	}
	switch issue.Kind {
	case IssueMergedUnlanded, IssueMergedStale:
		trunk := plan.Snapshot.Trunk()
		// A former feature base may contain the head, so bound at the landing
		// trunk rather than excluding that work through the former base.
		trunkEvidence := landed.Unknown
		if !slices.Contains(plan.Absent, trunk) {
			own := []string{plan.SitsOn(issue.Branch)}
			if previous != nil {
				own = append(own, previous.HeadOID)
			}
			trunkEvidence = landed.AssessOwn(ctx, s.Tips, trunk, issue.Branch, trunk, own...)
		}
		switch trunkEvidence {
		case landed.Present:
			issue.Kind, issue.Reason = IssueMerged, "PR merged; landed in "+trunk
		case landed.Absent:
			if issue.Kind == IssueMergedUnlanded && previous != nil {
				issue.Reason = "merged into " + previous.Base + ", not landed"
			}
		}
		parent := plan.SitsOn(issue.Branch)
		if issue.Kind != IssueMerged && previous != nil && previous.Base == parent && parent != trunk {
			absorption := landed.AssessParent(ctx, s.Tips, parent, issue.Branch, knownParent)
			if absorption.Present() {
				issue.Kind, issue.Reason = IssueAbsorbed, "merged into "+parent+"; absorbed"
				if absorption.Local != landed.Present {
					issue.Kind = IssueAbsorbedStale
					issue.Reason += " in the known published parent; refresh " + parent + " before pruning"
				}
			}
		}
	case IssueMissing, IssueClosed:
		below := plan.SitsOn(issue.Branch)
		if slices.Contains(plan.Absent, below) {
			return issue
		}
		if landed.Assess(ctx, s.Tips, below, issue.Branch, below) == landed.Present {
			issue.Kind, issue.Reason = IssueLanded, "landed in "+below
		}
	}
	return issue
}

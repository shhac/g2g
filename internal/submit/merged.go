package submit

import (
	"context"
	"slices"

	"github.com/shhac/g2g/internal/landed"
	"github.com/shhac/g2g/internal/parallel"
)

// mergedAbsorbed is distinct from landing in the trunk: a merge into the
// recorded feature parent needs no replacement PR when that parent has the
// work. Read known published parents once before independent content probes.
func (s Service) mergedAbsorbed(ctx context.Context, plan Plan) map[string]landed.ParentAbsorption {
	probe, ok := s.Git.(landed.Probe)
	if !ok {
		return nil
	}
	branches, parents := make([]string, 0), make([]string, 0)
	for _, branch := range plan.Snapshot.Branches {
		previous := plan.Superseded[branch]
		parent := plan.Snapshot.SitsOn(branch)
		if previous.State != "MERGED" || previous.Base != parent || parent == plan.Snapshot.Trunk() || plan.MergedLanded[branch] {
			continue
		}
		branches = append(branches, branch)
		if !slices.Contains(parents, parent) {
			parents = append(parents, parent)
		}
	}
	tips := landed.KnownParents(ctx, s.Git, plan.Remote, parents)
	answers := make([]landed.ParentAbsorption, len(branches))
	_ = parallel.Each(ctx, branches, func(ctx context.Context, index int, branch string) error {
		parent := plan.Snapshot.SitsOn(branch)
		answers[index] = landed.AssessParent(ctx, probe, parent, branch, tips[parent])
		return nil
	})
	result := map[string]landed.ParentAbsorption{}
	for index, answer := range answers {
		if answer.Present() {
			result[branches[index]] = answer
		}
	}
	return result
}

// A merged predecessor is lifecycle history, not proof that work reached the
// trunk. Only merged predecessors need this optional, local content reading.
func (s Service) mergedLanded(ctx context.Context, plan Plan) map[string]bool {
	probe, ok := s.Git.(landed.Probe)
	if !ok {
		return nil
	}
	trunk := plan.Snapshot.Trunk()
	if trunk == "" {
		return nil
	}
	answers := make([]landed.Evidence, len(plan.Snapshot.Branches))
	_ = parallel.Each(ctx, plan.Snapshot.Branches, func(ctx context.Context, index int, branch string) error {
		if plan.Superseded[branch].State != "MERGED" {
			return nil
		}
		// The former feature base may contain the head now, so bound at the
		// trunk rather than excluding the head through that feature base.
		answers[index] = landed.Assess(ctx, probe, trunk, branch, trunk)
		return nil
	})
	result := map[string]bool{}
	for index, answer := range answers {
		if answer != landed.Unknown {
			result[plan.Snapshot.Branches[index]] = answer == landed.Present
		}
	}
	return result
}

package submit

import (
	"context"

	"github.com/shhac/g2g/internal/landed"
	"github.com/shhac/g2g/internal/parallel"
)

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

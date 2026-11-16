package submit

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/landed"
	"github.com/shhac/g2g/internal/link"
	"github.com/shhac/g2g/internal/parallel"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/stack"
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
	unobserved := slices.DeleteFunc(slices.Clone(parents), func(parent string) bool { return slices.Contains(plan.Push.Branches, parent) })
	tips := maps.Clone(landed.KnownParents(ctx, s.Git, plan.Remote, unobserved))
	if tips == nil {
		tips = map[string]string{}
	}
	pushed := map[string]bool{}
	for _, lease := range plan.Push.Leases() {
		pushed[lease.Branch] = true
	}
	// Push's observed answer, including absence, outranks older local knowledge.
	for _, parent := range plan.Push.Branches {
		tips[parent] = plan.Push.RemoteTips[parent]
	}
	answers := make([]landed.ParentAbsorption, len(branches))
	_ = parallel.Each(ctx, branches, func(ctx context.Context, index int, branch string) error {
		parent := plan.Snapshot.SitsOn(branch)
		answers[index] = landed.AssessParent(ctx, probe, parent, branch, "")
		answers[index].Known = landed.AssessPublishedParent(ctx, probe, branch, tips[parent])
		return nil
	})
	result := map[string]landed.ParentAbsorption{}
	for index, answer := range answers {
		parent := plan.Snapshot.SitsOn(branches[index])
		_, omittedParent := result[parent]
		if answer.ForPublication(pushed[parent] && !omittedParent) {
			result[branches[index]] = answer
		}
	}
	return result
}

// Publication is an action filter over the selected topology. Keep the parent
// map intact so a surviving branch cannot silently acquire an invented base.
func (p Plan) Publication() stack.Snapshot {
	snapshot := p.Snapshot
	snapshot.Branches = slices.DeleteFunc(slices.Clone(snapshot.Branches), func(branch string) bool { _, absorbed := p.Absorbed[branch]; return absorbed })
	return snapshot
}

func (p Plan) absorbedBaseRepair(linking bool) repair.Note {
	resolutions := githubstack.ResolveHeads(p.Existing)
	for _, branch := range p.Publication().Branches {
		base := p.Snapshot.SitsOn(branch)
		if _, absorbed := p.Absorbed[base]; !absorbed {
			continue
		}
		if !linking && resolutions[branch].Open != nil {
			continue
		}
		return repair.Note{Reason: fmt.Sprintf("%s depends on absorbed base %s", branch, base), Ways: link.AbsorbedWays(p.Snapshot, base, p.Absorbed[base].Local != landed.Present, p.Remote)}
	}
	return repair.Note{}
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

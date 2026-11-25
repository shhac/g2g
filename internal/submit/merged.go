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
	snapshot.Branches = slices.DeleteFunc(slices.Clone(snapshot.Branches), p.omitted)
	return snapshot
}

// omitted is a selected branch with nothing left to submit: its work is in
// the branch it was merged into, or in the trunk.
func (p Plan) omitted(branch string) bool {
	_, absorbed := p.Absorbed[branch]
	return absorbed || p.landed(branch)
}

// landed is a branch whose pull request merged and whose work the landing
// trunk holds, by content. It is the merged-history answer submit already
// reads, so no further question is asked of Git or GitHub.
//
// Such a branch is still selected after a pull and before a prune, because
// pull collapses it and leaves forgetting it to prune. Treating it as a branch
// whose pull request needed replacing asked for a title for work already in
// the trunk, and refused the branches above it until one was written. A merge
// with no confirmed content stays a replacement or a refresh: lifecycle alone
// is not landing.
func (p Plan) landed(branch string) bool {
	return p.Superseded[branch].Merged() && p.MergedLanded[branch]
}

// Landed are the selected branches left out because the trunk has their
// work, in selection order.
func (p Plan) Landed() []string {
	return slices.DeleteFunc(slices.Clone(p.Snapshot.Branches), func(branch string) bool { return !p.landed(branch) })
}

// OnLanded are the open pull requests a submission publishes to that are
// still based on a landed branch it leaves out. submit preserves an existing
// base and the record still names the landed branch, so nothing here moves
// them; they are named because the base they are left on is finished.
func (p Plan) OnLanded() []githubstack.PathStep {
	on := make([]githubstack.PathStep, 0)
	for step := range p.Publication().Steps(p.Existing) {
		if step.Resolution.Open != nil && p.landed(step.Resolution.Open.Base) {
			on = append(on, step)
		}
	}
	return on
}

// omittedBaseRepair refuses to open or link a pull request across a base the
// submission leaves out. The record still names that base, so a new pull
// request would open on a finished branch and a linked stack would have a
// gap; choosing another base here would be recording structure, which is the
// cleanup's job. An existing pull request keeps the base it has.
func (p Plan) omittedBaseRepair(linking bool) repair.Note {
	resolutions := githubstack.ResolveHeads(p.Existing)
	for _, branch := range p.Publication().Branches {
		base := p.Snapshot.SitsOn(branch)
		if !p.omitted(base) {
			continue
		}
		if !linking && resolutions[branch].Open != nil {
			continue
		}
		if p.landed(base) {
			return repair.Note{Reason: fmt.Sprintf("%s depends on landed base %s", branch, base), Ways: []repair.Step{p.forgetLanded(base)}}
		}
		return repair.Note{Reason: fmt.Sprintf("%s depends on absorbed base %s", branch, base), Ways: link.AbsorbedWays(p.Snapshot, base, p.Absorbed[base].Local != landed.Present, p.Remote)}
	}
	return repair.Note{}
}

// forgetLanded names the source's cleanup for a branch landed in the trunk.
//
// The prune is over that branch's stack, not the branch alone. A branch
// absorbed into its parent is judged against that parent, and pruning it
// alone is right. One landed in the trunk is judged through every landed
// branch under it, and when those have not been forgotten yet a prune of it
// alone asks whether it landed in the branch below, hears no, and forgets
// nothing.
func (p Plan) forgetLanded(branch string) repair.Step {
	forget := link.ForgetLanded(p.Snapshot.Source)
	if p.Snapshot.Source == stack.SourceG2G {
		forget.Command = repair.Command([]string{"g2g", "prune", "--branch", branch})
	}
	return forget
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
		// Where its own work starts is the branch it sits on, or the head
		// that merged: commits added since, or ones that take back part of
		// what the branch below added, are not in a merge of the whole.
		answers[index] = landed.AssessOwn(ctx, probe, trunk, branch, trunk, plan.Snapshot.SitsOn(branch), plan.Superseded[branch].HeadOID)
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

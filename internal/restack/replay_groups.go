package restack

import (
	localgit "github.com/shhac/g2g/internal/git"
)

// replayGroup is one independent replay: a root whose parent is not itself
// being replayed, and every branch replayed above it. Its steps arrive parents
// before children, so the first is the root.
type replayGroup []Step

// ranges are what the replay engine is given for one group.
//
// Every range starts at the group root's fork point rather than at each
// branch's own. The engine replays the union of the ranges onto one base and
// updates each named ref, so a chain has to be expressed as overlapping ranges
// from a single origin; per-branch origins ask it to place each branch
// directly on the base independently, which conflicts as soon as a branch
// depends on the one below it.
func (g replayGroup) ranges() []localgit.Range {
	origin := g[0].ForkPoint
	ranges := make([]localgit.Range, 0, len(g))
	for _, step := range g {
		ranges = append(ranges, localgit.Range{From: origin, To: step.Branch})
	}
	return ranges
}

// previewed are the ranges as they will be when the rewrite runs. A branch the
// caller moves first is named by where it is going, because naming the branch
// would preview the version about to be replaced.
func (g replayGroup) previewed() []localgit.Range {
	ranges := g.ranges()
	for index, step := range g {
		if step.Head != "" && step.Head != step.Tip {
			ranges[index].To = step.Head
		}
	}
	return ranges
}

// onto is what the group lands on: its root's base, once any collapse below it
// has been accounted for. A root that is behind its parent lands on wherever
// the parent's own replay has just put it, so it names the parent rather than
// an object that will be stale by then.
func (g replayGroup) onto() string {
	if g[0].Behind {
		return g[0].Parent
	}
	return g[0].Base
}

// groups splits the replay into one invocation per independent root.
//
// One origin and one base are only right for a single line of descent and
// what forks from it. Two roots -- the stacks of a --scope trunk, or a subtree
// whose root collapsed and left two children -- fork at different points and
// can land on different bases, and giving them the first root's origin
// widened the second's range to take in the trunk's own commits, replaying
// onto the first root's base a stale copy of work the second had rewritten.
//
// A branch behind its parent is a root of its own for the same reason: the
// engine keeps each commit on the replayed copy of its own parent, so sharing
// the parent's replay put the branch back on the commit it forked from.
// Groups come out parents first, which is the order they have to run in.
func (p Plan) groups() []replayGroup {
	groups := make([]replayGroup, 0, 1)
	member := map[string]int{}
	for _, step := range p.rewriting() {
		if at, above := member[step.Parent]; above && !step.Behind {
			groups[at] = append(groups[at], step)
			member[step.Branch] = at
			continue
		}
		member[step.Branch] = len(groups)
		groups = append(groups, replayGroup{step})
	}
	return groups
}

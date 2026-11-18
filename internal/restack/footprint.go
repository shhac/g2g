package restack

import (
	"maps"
	"slices"
)

// Footprint is what an operation moves and what it reads: the branches it may
// rewrite or record anew, and those with the parents they are measured
// against or replayed onto. Two operations in different worktrees run beside
// each other unless either moves what the other reads.
//
// Between the two is a branch an operation only advances: it ends on a commit
// that contains where it was. That is a trunk a pull fast-forwards, and it is
// apart from a restack that replays onto that trunk. What the restack has
// replayed still sits on a commit the trunk contains, the fork point it
// recorded is still the trunk's history, and a branch behind its trunk is the
// state every stack is in the moment anything lands. Finishing the restack
// plans again from the refs and carries on to where the trunk is now; undoing
// it puts back tips and fork points that never named the trunk's tip.
// Refusing it made every pull wait for every stack's conflict, since every
// stack reads the trunk. An advance still meets what another moves, as any
// read does: nothing may fast-forward a branch a restack is rewriting.
type Footprint struct {
	moves, reads map[string]bool
}

// Reads is the footprint of a command that moves nothing a restack replays
// onto, and reads branches: the ones it publishes or records, with the
// parents it measures them against.
func Reads(branches ...string) Footprint {
	return Footprint{reads: members(branches)}
}

// Moves is the footprint of a command other than a restack that rewrites
// branches, deletes them, or changes or forgets what the graph records of
// them. It reads them too.
func Moves(branches ...string) Footprint {
	return Footprint{moves: members(branches), reads: members(branches)}
}

// Advances is the footprint of fast-forwarding branches: each ends on a
// commit that contains where it was. To everything else that is a read. What
// another operation reads of the branch is still true of it afterwards, and
// one that moves the branch already meets anything that reads it. It is
// named so that a caller says which it means, and a fast-forward cannot be
// declared a move for want of a word.
func Advances(branches ...string) Footprint { return Reads(branches...) }

// With is both footprints together, for a command made of several steps.
func (f Footprint) With(other Footprint) Footprint {
	return Footprint{moves: both(f.moves, other.moves), reads: both(f.reads, other.reads)}
}

// members is the named branches as a set, less the empty name a missing
// parent or an unset flag leaves.
func members(branches []string) map[string]bool {
	set := make(map[string]bool, len(branches))
	for _, branch := range branches {
		if branch != "" {
			set[branch] = true
		}
	}
	return set
}

func both(one, other map[string]bool) map[string]bool {
	joined := make(map[string]bool, len(one)+len(other))
	maps.Copy(joined, one)
	maps.Copy(joined, other)
	return joined
}

// footprintOf is a footprint from what a restack moves, the parent each of
// them is recorded on, where it reparents any of them, and the branch an
// --onto names. A plan and the record it journals both go through it, so what
// a restack is admitted by and what the others weigh it by are one set.
func footprintOf(moves map[string]bool, parents, reparent map[string]string, onto string) Footprint {
	reads := maps.Clone(moves)
	for _, parent := range parents {
		reads[parent] = true
	}
	for _, parent := range reparent {
		reads[parent] = true
	}
	reads[onto] = true
	delete(reads, "")
	return Footprint{moves: moves, reads: reads}
}

// Footprint is what applying the plan moves and reads: every selected branch
// it records, since recording walks the whole selection, with their parents,
// where it reparents them, and the branch an --onto names. A trunk in the
// selection is read and never moved, so two stacks on the same trunk restack
// beside each other.
func (p Plan) Footprint() Footprint {
	moves, parents := map[string]bool{}, map[string]string{}
	for _, branch := range p.Discovery.Branches {
		if edge, tracked := p.Discovery.Graph.Edges[branch]; tracked {
			moves[branch], parents[branch] = true, edge.Parent
		}
	}
	return footprintOf(moves, parents, p.reparenting(), p.Onto.Parent)
}

// footprint is what the restack this record journals moves and reads.
func (r Record) footprint() Footprint {
	parents := make(map[string]string, len(r.Structure))
	for branch, edge := range r.Structure {
		parents[branch] = edge.Parent
	}
	return footprintOf(r.moving(), parents, r.Reparent, r.OntoParent)
}

// moving is every branch the restack may move, whether or not it has yet.
func (r Record) moving() map[string]bool {
	moving := map[string]bool{}
	for branch := range r.Original {
		moving[branch] = true
	}
	for branch := range r.Reparent {
		moving[branch] = true
	}
	for branch := range r.Structure {
		moving[branch] = true
	}
	delete(moving, "")
	return moving
}

// meets is every branch one footprint moves that the other reads, either way
// round, sorted.
func (f Footprint) meets(other Footprint) []string {
	shared := map[string]bool{}
	f.holds(other, shared)
	other.holds(f, shared)
	return slices.Sorted(maps.Keys(shared))
}

// holds adds to shared what this footprint moves that the other reads.
func (f Footprint) holds(other Footprint, shared map[string]bool) {
	for branch := range f.moves {
		if other.reads[branch] {
			shared[branch] = true
		}
	}
}

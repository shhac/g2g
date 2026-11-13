package restack

import (
	"maps"
	"slices"
)

// Footprint is what an operation moves and what it reads: the branches it may
// rewrite or record, and those with the parents they are measured against or
// replayed onto. Two restacks in different worktrees run beside each other
// unless either moves what the other reads. Only a restack declares what it
// moves; every other command reads (Reads).
type Footprint struct {
	moves, reads map[string]bool
}

// Reads is the footprint of a command that moves nothing a restack replays
// onto, and reads branches: the ones it publishes or records, with the
// parents it measures them against.
func Reads(branches ...string) Footprint {
	reads := make(map[string]bool, len(branches))
	for _, branch := range branches {
		if branch != "" {
			reads[branch] = true
		}
	}
	return Footprint{reads: reads}
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
	for branch := range f.moves {
		if other.reads[branch] {
			shared[branch] = true
		}
	}
	for branch := range other.moves {
		if f.reads[branch] {
			shared[branch] = true
		}
	}
	return slices.Sorted(maps.Keys(shared))
}

package shape

import "sort"

// BreadthFirst returns from and everything under it, a generation at a time:
// every parent before any of its children, and siblings in sorted order.
//
// Writing to Graphite needs this order, because gt track refuses a parent it
// does not already track. Two callers carried their own copy of the walk and
// only one remembered which branches it had visited; the other relied on the
// forest never presenting a branch twice, which a parsed display is not
// obliged to honour. Each branch is visited once here, so no shape of input
// can make the walk repeat itself or fail to finish.
//
// The seeds are taken in the order given, so a caller chooses which roots are
// authoritative — the ones a display named, or the ones parentage implies.
func (f Forest) BreadthFirst(from []string) []string {
	children := make(map[string][]string, len(f.Parents))
	for branch, parent := range f.Parents {
		if parent != "" && parent != branch {
			children[parent] = append(children[parent], branch)
		}
	}
	for _, siblings := range children {
		sort.Strings(siblings)
	}

	seen := make(map[string]bool, len(f.Parents)+len(from))
	ordered := make([]string, 0, len(f.Parents)+len(from))
	queue := append([]string(nil), from...)
	for len(queue) != 0 {
		branch := queue[0]
		queue = queue[1:]
		if seen[branch] {
			continue
		}
		seen[branch] = true
		ordered = append(ordered, branch)
		queue = append(queue, children[branch]...)
	}
	return ordered
}

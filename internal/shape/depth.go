package shape

// Depths returns each node's indent depth, keyed by branch, for a selection
// already ordered parent-before-child. A parent outside the selection does not
// count: the selection's own roots sit at depth zero.
//
// A selection where no branch has two selected children is a chain, and a chain
// reads better as the flat list every other command shows than as a staircase
// that pushes each branch further right for no information.
//
// The graph views, status, and the stack comment on a pull request all ask this
// question. Answering it twice is exactly how graph and status came to disagree
// — one suppressed the staircase for a chain and the other did not, so the same
// shape rendered two ways depending on which command you asked.
func Depths(ordered []string, parentOf func(string) (string, bool)) map[string]int {
	within := make(map[string]bool, len(ordered))
	for _, branch := range ordered {
		within[branch] = true
	}
	selectedParent := func(branch string) (string, bool) {
		parent, tracked := parentOf(branch)
		if !tracked || !within[parent] {
			return "", false
		}
		return parent, true
	}

	children := make(map[string]int, len(ordered))
	forked := false
	for _, branch := range ordered {
		parent, ok := selectedParent(branch)
		if !ok {
			continue
		}
		children[parent]++
		if children[parent] > 1 {
			forked = true
		}
	}
	if !forked {
		return nil
	}

	depths := make(map[string]int, len(ordered))
	for _, branch := range ordered {
		if parent, ok := selectedParent(branch); ok {
			depths[branch] = depths[parent] + 1
		}
	}
	return depths
}

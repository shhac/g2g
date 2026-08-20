package cli

import "strings"

// Drawing a forest as text.
//
// Pure functions over depths and parentage: no Presentation, no writer, and
// nothing from stackView beyond stackNode.Depth. Three commands draw the same
// shape — graph, status and the graph preview — so this is the layout they
// share, and it can be tested as the algorithm it is rather than through
// whichever command happens to call it.

// treeDepths returns each node's indent depth, keyed by branch, for a selection
// already ordered parent-before-child. A parent outside the selection does not
// count: the selection's own roots sit at depth zero.
//
// A selection where no branch has two selected children is a chain, and a chain
// reads better as the flat list every other command shows than as a staircase
// that pushes each branch further right for no information.
//
// Both the graph views and status ask this question. Answering it twice is
// exactly how they came to disagree — one suppressed the staircase for a chain
// and the other did not, so the same shape rendered two ways depending on which
// command you asked.
func treeDepths(ordered []string, parentOf func(string) (string, bool)) map[string]int {
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

// treePrefixes derives each node's connector from the pre-order depths alone,
// so the graph walk does not have to hand layout down to the renderer. A view
// with no depth gets empty prefixes and renders exactly as it always has.
func treePrefixes(nodes []stackNode) []string {
	prefixes := make([]string, len(nodes))
	for index, node := range nodes {
		if node.Depth == 0 {
			continue
		}
		connector := lastGlyph
		if continues(nodes, index, node.Depth) {
			connector = forkGlyph
		}
		prefixes[index] = rails(nodes, index) + connector
	}
	return prefixes
}

// rails draws the ancestor lines a node hangs under. A level continues only
// while a later node still sits at it; once that subtree closes the rail stops
// and the space keeps the names aligned.
func rails(nodes []stackNode, index int) string {
	var prefix strings.Builder
	for level := 1; level < nodes[index].Depth; level++ {
		if continues(nodes, index, level) {
			prefix.WriteString(railGlyph + " ")
			continue
		}
		prefix.WriteString("  ")
	}
	return prefix.String()
}

// continues reports whether a later sibling exists at depth before the
// enclosing subtree ends.
func continues(nodes []stackNode, from, depth int) bool {
	for index := from + 1; index < len(nodes); index++ {
		if nodes[index].Depth < depth {
			return false
		}
		if nodes[index].Depth == depth {
			return true
		}
	}
	return false
}

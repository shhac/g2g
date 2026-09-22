package cli

import "strings"

// Drawing a forest as text.
//
// Pure functions over depths: no Presentation, no writer, and nothing from
// stackView beyond stackNode.Depth. How deep each node sits is shape.Depths,
// which the pull request comment asks too; this is only how a terminal draws
// the answer.

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

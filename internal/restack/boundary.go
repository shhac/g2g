package restack

import "github.com/shhac/g2g/internal/graph"

// aboveDiscovery narrows the existing selection rather than selecting a new
// subtree: a target's unselected uncles must never enter a bounded replay.
func aboveDiscovery(discovery graph.Discovery, above string) graph.Discovery {
	if above == "" {
		return discovery
	}
	members := make(map[string]bool)
	for _, branch := range discovery.Graph.Shape().Subtree(above) {
		members[branch] = true
	}
	selected := make([]string, 0, len(discovery.Branches))
	for _, branch := range discovery.Branches {
		if branch != above && members[branch] {
			selected = append(selected, branch)
		}
	}
	discovery.Branches = selected
	return discovery
}

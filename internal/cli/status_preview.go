package cli

import (
	"fmt"
	"slices"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
)

func statusView(discovery graph.Discovery) stackView {
	view := graphView(discovery, "status")
	// A trunk has no recorded parent because it is a root, which the node above
	// already says. Telling someone standing on it to adopt one contradicts the
	// line they just read and names a command that would refuse.
	//
	// The graph's own trunks are branches nothing sits under, so an empty store
	// has none — which is exactly the repository where somebody standing on
	// main was told to give it a parent. What the remote calls its default
	// closes that gap without the store having to know anything yet.
	untracked := !discovery.Graph.Tracked(discovery.Target)
	// Only when nothing is stacked on it. This told anybody standing on main to
	// start a stack there, on every read, including ones showing the forest
	// already on it — advice for an empty trunk, given to a full one.
	if untracked {
		if note := untrackedNote(discovery); note != "" {
			view = view.note(note, severityNeutral)
		}
	}
	if hidden := hiddenDescendants(discovery); hidden != 0 {
		view = view.note(fmt.Sprintf("%s below this one not shown · rerun with --scope subtree, or --scope all for every stack.", count(hidden, "branch", "branches")), severityNeutral)
	}
	return driftNotes(view, discovery)
}

// hiddenDescendants counts what the selected scope left out below the target.
// Standing on a trunk with the default scope shows one node and no hint that
// nine stacks hang off it, which reads as an empty graph rather than a narrow
// question.
func hiddenDescendants(discovery graph.Discovery) int {
	selected := make(map[string]bool, len(discovery.Branches))
	for _, branch := range discovery.Branches {
		selected[branch] = true
	}
	hidden := 0
	for _, branch := range discovery.Graph.Subtree(discovery.Target) {
		// Subtree includes the target, which is never "below" itself. Counting
		// it made an unrecorded branch claim one hidden descendant.
		if branch != discovery.Target && !selected[branch] {
			hidden++
		}
	}
	return hidden
}

// isTrunk reports a target nothing should be asked to hang under: the graph
// already treats it as a root, or the remote calls it the default branch.
func isTrunk(discovery graph.Discovery) bool {
	return discovery.Graph.IsTrunk(discovery.Target) || discovery.Target == discovery.DefaultTrunk
}

// untrackedNote says the one true thing about a target the graph does not
// record, which differs by what is around it.
//
// Three states used to be two. A trunk with a forest already on it was told to
// start a stack there, which is advice for an empty one; and a target absent
// from the drawing was named in the header, omitted from the tree, and
// explained by a note about parents — which reads as a rendering fault rather
// than as the branch simply not being recorded.
func untrackedNote(discovery graph.Discovery) string {
	switch {
	case isTrunk(discovery):
		if len(discovery.Graph.Children(discovery.Target)) != 0 {
			// Nothing sits under a trunk, and this one plainly is one: there is
			// a forest drawn on it. Telling its owner to record a parent for it
			// contradicts the picture they are looking at.
			return ""
		}
		return fmt.Sprintf("%s is this repository's default branch · stack on it with %s.", discovery.Target, runnable("g2g track --branch <child> --parent "+discovery.Target))
	case !slices.Contains(discovery.Branches, discovery.Target):
		// Not in the drawing at all. A trunk is untracked and still drawn, so
		// the question is what the selection contains rather than whether the
		// graph records an edge. The widest scopes are where this shows: all
		// promises every stack, so a reader has no reason to suspect the one
		// they are standing on is missing.
		return fmt.Sprintf("%s is not in the graph, so it is not drawn above · run %s to record it.", discovery.Target, runnable("g2g track"))
	default:
		return "This branch has no recorded parent · run " + runnable("g2g track") + " to adopt one."
	}
}

// sourceGraphView draws the shape and nothing else.
//
// The state g2g's own view annotates — needs restack, moved off parent, fork
// point lost — is computed from recorded fork points, which only g2g's store
// has. Another record describes where branches sit and cannot describe whether
// their contents have drifted, so this says the first and stays silent on the
// second rather than inventing an answer.
func sourceGraphView(snapshot stack.Snapshot) stackView {
	ordered := append([]string{snapshot.Base}, snapshot.Branches...)
	depths := shape.Depths(ordered, snapshot.ParentOf)
	nodes := []stackNode{{Branch: snapshot.Base, Trunk: true}}
	for _, branch := range snapshot.Branches {
		nodes = append(nodes, stackNode{
			Branch: branch,
			Target: branch == snapshot.Target,
			Parent: snapshot.Parents[branch],
			Depth:  depths[branch],
		})
	}
	return stackView{Operation: "status", Target: snapshot.Target, TargetSource: snapshot.TargetSource, Nodes: nodes}
}

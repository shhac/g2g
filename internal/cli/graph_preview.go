package cli

import (
	"fmt"
	"io"
	"slices"

	"github.com/shhac/g2g/internal/graph"
)

// graphNodes projects a discovery onto the shared view.
//
// Depth is only populated when the selection actually forks. A selection where
// no branch has two selected children is a chain, and a chain reads better as
// the flat list every other command shows than as a staircase that pushes each
// branch further right for no information.
func graphNodes(discovery graph.Discovery) []stackNode {
	depths := treeDepths(discovery.Branches, discovery.Graph.Parent)
	nodes := make([]stackNode, 0, len(discovery.Branches))
	for index, branch := range discovery.Branches {
		parent, _ := discovery.Graph.Parent(branch)
		node := stackNode{
			Branch: branch,
			Parent: parent,
			Depth:  depths[branch],
			Target: branch == discovery.Target,
			// The root of a path or component is the base the graph hangs
			// from. A lone untracked branch is not a base, it is the branch
			// being asked about, so a single-node view has no trunk.
			Trunk: discovery.Graph.IsTrunk(branch) || (index == 0 && len(discovery.Branches) > 1 && !discovery.Graph.Tracked(branch)),
		}
		node.State, node.Severity = nodeState(discovery, branch)
		nodes = append(nodes, node)
	}
	return nodes
}

// nodeState says what the graph knows about one branch without a network call.
func nodeState(discovery graph.Discovery, branch string) (string, severity) {
	switch discovery.States[branch] {
	case graph.StateNeedsRestack:
		return "needs restack", severityWarn
	case graph.StateMovedOffParent:
		return "moved off parent", severityWarn
	case graph.StateForkUnresolvable:
		return "fork point lost", severityBad
	case graph.StateParentMissing:
		return "parent missing", severityWarn
	case graph.StateLanded:
		return "landed", severityOK
	case graph.StateEmpty:
		return "no commits of its own", severityNeutral
	case graph.StateUntracked:
		if discovery.Graph.IsTrunk(branch) {
			return "trunk", severityNeutral
		}
		return "untracked", severityNeutral
	default:
		return "", severityNeutral
	}
}

func graphView(discovery graph.Discovery, operation string) stackView {
	return stackView{
		Operation:    operation,
		Target:       discovery.Target,
		TargetSource: discovery.TargetSource,
		Nodes:        graphNodes(discovery),
	}
}

// writeGraphView renders a graph view and appends the store line to it.
//
// That line goes last in every one of these views — it says where the graph
// lives, which is worth being able to find and is not what the reader came
// for. Adding it here rather than at each view's return means no branch of any
// of them can forget it.
func writeGraphView(writer io.Writer, view stackView, discovery graph.Discovery, p Presentation) error {
	summary := fmt.Sprintf("Scope %s · %s · %s", discovery.Scope, count(len(discovery.Branches), "branch", "branches"), discovery.StorePath)
	return writeStackView(writer, view.note(summary, severityNeutral), p)
}

func count(total int, singular, plural string) string {
	return fmt.Sprintf("%d %s", total, pick(total, singular, plural))
}

// pick chooses the form that agrees with a count. Two helpers were doing this,
// one counting and one not, which is one idea wearing two names.
func pick(total int, one, many string) string {
	if total == 1 {
		return one
	}
	return many
}

// driftNotes report what g2g can see and deliberately will not repair.
func driftNotes(view stackView, discovery graph.Discovery) stackView {
	if stale := discovery.NeedsRestack(); len(stale) != 0 {
		view = view.note("Parent moved under "+branchList(stale)+" · run "+runnable("g2g restack")+".", severityWarn)
	}
	if moved := discovery.InState(graph.StateMovedOffParent); len(moved) != 0 {
		view = view.note("No longer built on the recorded parent: "+branchList(moved)+" · retrack before restacking, the replay range would be wrong.", severityWarn)
	}
	if lost := discovery.InState(graph.StateForkUnresolvable); len(lost) != 0 {
		view = view.note("Recorded fork point is gone for "+branchList(lost)+" · retrack to record it again.", severityBad)
	}
	if missing := discovery.MissingParents(); len(missing) != 0 {
		view = view.note("Recorded parent is no longer a local branch for "+branchList(missing)+" · retrack onto its new parent.", severityWarn)
	}
	if landed := discovery.InState(graph.StateLanded); len(landed) != 0 {
		view = view.note("Already in the trunk: "+branchList(landed)+" · run "+runnable("g2g prune")+" to forget them.", severityNeutral)
	}
	if empty := discovery.InState(graph.StateEmpty); len(empty) != 0 {
		view = view.note("Nothing of their own on "+branchList(empty)+" · either finished, or not started yet.", severityNeutral)
	}
	if orphans := discovery.Orphans(); len(orphans) != 0 {
		view = view.note("No tracked parent for "+branchList(orphans)+".", severityWarn)
	}
	return view
}

func graphStatusView(discovery graph.Discovery) stackView {
	view := graphView(discovery, "graph")
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

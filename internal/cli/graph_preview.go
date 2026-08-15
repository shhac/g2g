package cli

import (
	"fmt"
	"strings"

	"github.com/shhac/gt2gh/internal/graph"
)

// graphNodes projects a discovery onto the shared view.
//
// Depth is only populated when the selection actually forks. A selection where
// no branch has two selected children is a chain, and a chain reads better as
// the flat list every other command shows than as a staircase that pushes each
// branch further right for no information.
func graphNodes(discovery graph.Discovery) []stackNode {
	depths := renderDepths(discovery)
	nodes := make([]stackNode, 0, len(discovery.Branches))
	for index, branch := range discovery.Branches {
		parent, _ := discovery.Graph.Parent(branch)
		node := stackNode{
			Branch: branch,
			Parent: parent,
			Depth:  depths[index],
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
	case graph.StateParentMissing:
		return "parent missing", severityWarn
	case graph.StateUntracked:
		if discovery.Graph.IsTrunk(branch) {
			return "trunk", severityNeutral
		}
		return "untracked", severityNeutral
	default:
		return "", severityNeutral
	}
}

// renderDepths returns the indentation depth of each selected branch, or all
// zeros when the selection is a chain.
func renderDepths(discovery graph.Discovery) []int {
	selected := make(map[string]int, len(discovery.Branches))
	for index, branch := range discovery.Branches {
		selected[branch] = index
	}
	children := make(map[string]int, len(discovery.Branches))
	forked := false
	for _, branch := range discovery.Branches {
		parent, tracked := discovery.Graph.Parent(branch)
		if !tracked {
			continue
		}
		if _, inSelection := selected[parent]; !inSelection {
			continue
		}
		children[parent]++
		if children[parent] > 1 {
			forked = true
		}
	}

	depths := make([]int, len(discovery.Branches))
	if !forked {
		return depths
	}
	for index, branch := range discovery.Branches {
		parent, tracked := discovery.Graph.Parent(branch)
		parentIndex, inSelection := selected[parent]
		if tracked && inSelection {
			depths[index] = depths[parentIndex] + 1
		}
	}
	return depths
}

func graphView(discovery graph.Discovery, operation string) stackView {
	return stackView{
		Operation:    operation,
		Target:       discovery.Target,
		TargetSource: discovery.TargetSource,
		Nodes:        graphNodes(discovery),
	}
}

// storeNote goes last in every view: it says where the graph lives, which is
// worth being able to find and is not what the reader came for.
func storeNote(view stackView, discovery graph.Discovery) stackView {
	return view.note(fmt.Sprintf("Scope %s · %s · %s", discovery.Scope, countBranches(len(discovery.Branches)), discovery.StorePath), severityNeutral)
}

func countBranches(total int) string { return count(total, "branch", "branches") }

func count(total int, singular, plural string) string {
	if total == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", total, plural)
}

// driftNotes report what gt2gh can see and deliberately will not repair.
func driftNotes(view stackView, discovery graph.Discovery) stackView {
	if stale := discovery.NeedsRestack(); len(stale) != 0 {
		view = view.note("Parent moved under "+branchList(stale)+" · gt2gh records structure and does not rebase.", severityWarn)
	}
	if missing := discovery.MissingParents(); len(missing) != 0 {
		view = view.note("Recorded parent is no longer a local branch for "+branchList(missing)+" · retrack onto its new parent.", severityWarn)
	}
	if orphans := discovery.Orphans(); len(orphans) != 0 {
		view = view.note("No tracked parent for "+branchList(orphans)+".", severityWarn)
	}
	return view
}

func graphStatusView(discovery graph.Discovery) stackView {
	view := graphView(discovery, "graph")
	if !discovery.Graph.Tracked(discovery.Target) {
		view = view.note("This branch has no recorded parent · run g2g track to adopt one.", severityNeutral)
	}
	return storeNote(driftNotes(view, discovery), discovery)
}

func trackView(plan graph.TrackPlan) stackView {
	view := driftNotes(graphView(plan.Discovery, "track"), plan.Discovery)
	if plan.Blocked == "" {
		view = view.note(fmt.Sprintf("Records %s under %s.", plan.Target, plan.Parent), severityOK)
		if plan.NewTrunk != "" {
			view = view.note(fmt.Sprintf("%s becomes a root of the graph.", plan.NewTrunk), severityNeutral)
		}
		return storeNote(view, plan.Discovery)
	}
	view = view.block("Apply blocked: " + plan.Blocked)
	return storeNote(view.note(candidateAdvice(plan), severityNeutral), plan.Discovery)
}

// candidateAdvice names the choice rather than making it. The nearest ancestor
// is usually the parent, and "usually" is not a basis for writing down a
// structure every later command trusts.
func candidateAdvice(plan graph.TrackPlan) string {
	if len(plan.Candidates) == 0 {
		return "No candidate parent found · pass --parent with a local branch to record one."
	}
	described := make([]string, 0, len(plan.Candidates))
	for _, candidate := range plan.Candidates {
		described = append(described, candidate.Branch+describeCandidate(candidate))
	}
	return "Candidate parents, nearest first: " + strings.Join(described, ", ") + " · rerun with --parent <branch>."
}

func describeCandidate(candidate graph.Candidate) string {
	switch {
	case candidate.Trunk && !candidate.Ancestor:
		return " (recorded root)"
	case candidate.Trunk:
		return " (root, " + count(candidate.Distance, "commit", "commits") + " behind)"
	default:
		return " (" + count(candidate.Distance, "commit", "commits") + " behind)"
	}
}

func untrackView(plan graph.UntrackPlan) stackView {
	view := graphView(plan.Discovery, "untrack")
	if len(plan.Removed) == 0 {
		return storeNote(view.note("No selected branch is tracked · nothing to remove.", severityNeutral), plan.Discovery)
	}
	view = view.note("Removes the recorded parent of "+branchList(plan.Removed)+".", severityOK)
	if len(plan.Orphaned) == 0 {
		return storeNote(view, plan.Discovery)
	}
	// Reparenting the children onto the grandparent would invent an edge the
	// user never asked for, so the consequence is shown instead.
	return storeNote(view.note("Leaves "+branchList(plan.Orphaned)+" without a tracked parent · they are not reparented.", severityWarn), plan.Discovery)
}

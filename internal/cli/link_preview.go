package cli

import (
	"slices"
	"strings"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/link"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/stack"
)

func linkView(plan link.Plan) stackView {
	issues := make(map[string]string, len(plan.Issues))
	for _, issue := range plan.Issues {
		issues[issue.Branch] = issue.Reason
	}
	prs := githubstack.ByHead(plan.PullRequests)

	view := stackView{
		Operation:    "github link",
		Target:       plan.Target,
		TargetSource: plan.TargetSource,
		Nodes:        []stackNode{{Branch: plan.Base, Trunk: true}},
	}
	for _, branch := range plan.Branches {
		node := stackNode{Branch: branch, Target: branch == plan.Target, PRNumber: prs[branch].Number, PRURL: prs[branch].URL}
		// The marker keeps an unresolved node self-describing without colour,
		// so redirected output still says why a branch cannot be linked.
		if reason := issues[branch]; reason != "" {
			node.PRNumber = 0
			node = node.labeled("unresolved: "+reason, severityBad)
		}
		view.Nodes = append(view.Nodes, node)
	}

	// A command is withheld only when none can be constructed: gh stack link
	// needs at least two branches. Being blocked is not that case — the command
	// is well formed, and showing it keeps the plan's destination visible for
	// triage, including running it by hand to see gh's own error.
	//
	// The count is checked directly rather than through NothingToLink, which
	// also folds in the issue check: a single-branch path that was blocked
	// therefore used to render a one-branch gh stack link that could never be
	// valid.
	if len(plan.Branches) >= 2 {
		view.Action = append([]string{"gh", "stack", "link", "--base", plan.Base}, plan.Branches...)
	}
	if len(plan.Issues) != 0 {
		blocked := view.blockedBy(blockedReason(plan))
		blocked.Repair, _ = linkRepair(plan)
		return blocked
	}
	if len(view.Action) == 0 {
		return view.note("Nothing to link — this stack has one pull request.", severityNeutral)
	}
	return view
}

// blockedReason is link's repair as the one sentence a machine reads, with
// each command marked so a person's renderer can draw it.
//
// It returns the reason without a label. status wants the same sentence under a
// different heading, and it used to get one by string-replacing the prefix back
// out of a rendered line — which quietly tied one command's output to a literal
// six other files typed by hand.
func blockedReason(plan link.Plan) string {
	note, _ := linkRepair(plan)
	return note.SentenceWith(runnable)
}

// linkRepair aims package-owned advice through the same selection seam as
// other CLI suggestions. A stale trunk is refreshed where status was aimed.
func linkRepair(plan link.Plan) (repair.Note, []link.IssueKind) {
	note, kinds := plan.Repair()
	// Absorbed cleanup already names each branch and its exact scope. Only
	// the generic trunk-refresh command needs aiming at this selection.
	if !slices.Contains(kinds, link.IssueMergedStale) {
		return note, kinds
	}
	command := selectedFrom(plan.Snapshot).aimedOr(pullCommand)
	note.Ways = slices.Clone(note.Ways)
	for index, way := range note.Ways {
		if way.Command == pullCommand.command {
			note.Ways[index].Command = command
		}
	}

	return note, kinds
}

// absorptionAdvice aims each source-aware cleanup command once. Package-owned
// policy determines the order; selection determines its flags.
func absorptionAdvice(snapshot stack.Snapshot, remote, branch string, stale bool) []repair.Step {
	ways := link.AbsorbedWays(snapshot, branch, stale, remote)
	if snapshot.Source != stack.SourceG2G {
		return ways
	}
	acted := selectedFrom(snapshot).from(remote).at(branch, "branch")
	for index, way := range ways {
		switch {
		case strings.HasPrefix(way.Command, "g2g pull "):
			ways[index].Command = acted.aimedOr(pullCommand) + " --above " + repair.Quote(snapshot.SitsOn(branch))
		case strings.HasPrefix(way.Command, "g2g restack "):
			ways[index].Command = acted.at(branch, "subtree").aimedOr(restackCommand)
		case strings.HasPrefix(way.Command, "g2g prune "):
			ways[index].Command = acted.narrowedTo("branch").aimedOr(pruneCommand)
		}
	}
	return ways
}

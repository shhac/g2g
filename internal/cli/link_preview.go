package cli

import (
	"strings"

	"github.com/shhac/gt2gh/internal/githubstack"
	"github.com/shhac/gt2gh/internal/link"
)

func linkView(plan link.Plan) stackView {
	issues := make(map[string]string, len(plan.Issues))
	for _, issue := range plan.Issues {
		issues[issue.Branch] = issue.Reason
	}
	prs := githubstack.ByHead(plan.PullRequests)

	view := stackView{
		Operation:    "link",
		Target:       plan.Target,
		TargetSource: plan.TargetSource,
		Nodes:        []stackNode{{Branch: plan.Base, Trunk: true}},
	}
	for _, branch := range plan.Branches {
		node := stackNode{Branch: branch, Target: branch == plan.Target, PRNumber: prs[branch].Number, PRURL: prs[branch].URL}
		// The marker keeps an unresolved node self-describing without colour,
		// so redirected output still says why a branch cannot be linked.
		if reason := issues[branch]; reason != "" {
			node.PRNumber, node.State, node.Severity = 0, "unresolved: "+reason, severityBad
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
		return view.block(blockedReason(plan))
	}
	if len(view.Action) == 0 {
		return view.note("Nothing to link — this stack has one pull request.", severityNeutral)
	}
	return view
}

// blockedReason names the command that actually repairs the state. link's
// policy requires every pull request to already sit on its Graphite
// predecessor, but reconciling exactly that is what sync is for, so a path
// blocked solely on bases would otherwise send the reader looking for a fix
// the tool already has.
func blockedReason(plan link.Plan) string {
	if plan.SyncRepairable() {
		return "Apply blocked: every pull request is open but based on the wrong branch. Run g2g sync to preview reconciling them."
	}
	return "Apply blocked: resolve every unresolved GitHub PR mapping first."
}

func commandText(command []string) string {
	parts := make([]string, len(command))
	for index, argument := range command {
		parts[index] = shellQuote(argument)
	}
	return strings.Join(parts, " ")
}

func shellQuote(argument string) string {
	if argument != "" && strings.IndexFunc(argument, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_+-./:=@", r))
	}) < 0 {
		return argument
	}
	return "'" + strings.ReplaceAll(argument, "'", "'\\''") + "'"
}

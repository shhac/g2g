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

	// A command is shown only when it would be accepted. Offering a copyable
	// gh invocation that apply would refuse invites running it by hand.
	if len(plan.Issues) != 0 {
		return view.note("Apply blocked: resolve every unresolved GitHub PR mapping first.", severityBad)
	}
	if plan.NothingToLink() {
		return view.note("Nothing to link — this stack has one pull request.", severityNeutral)
	}
	view.Action = append([]string{"gh", "stack", "link", "--base", plan.Base}, plan.Branches...)
	return view
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

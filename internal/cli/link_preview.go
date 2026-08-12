package cli

import (
	"strings"

	"github.com/shhac/gt2gh/internal/link"
)

// linkPreview is the semantic human-preview projection. It intentionally has
// no ANSI or writer dependency so another output format can consume the same
// validated plan without parsing terminal text.
type linkPreview struct {
	Target       string
	TargetSource string
	Nodes        []linkPreviewNode
	Command      []string
	ApplyBlocked bool
}

type linkPreviewNode struct {
	Branch     string
	Trunk      bool
	PRNumber   int
	Unresolved string
}

func newLinkPreview(plan link.Plan) linkPreview {
	issues := make(map[string]string, len(plan.Issues))
	for _, issue := range plan.Issues {
		issues[issue.Branch] = issue.Reason
	}
	prs := make(map[string]int, len(plan.PullRequests))
	for _, pr := range plan.PullRequests {
		prs[pr.Head] = pr.Number
	}
	preview := linkPreview{
		Target:       plan.Target,
		TargetSource: plan.TargetSource,
		Nodes:        []linkPreviewNode{{Branch: plan.Base, Trunk: true}},
		Command:      append([]string{"gh", "stack", "link", "--base", plan.Base}, plan.Branches...),
		ApplyBlocked: len(plan.Issues) != 0,
	}
	for _, branch := range plan.Branches {
		preview.Nodes = append(preview.Nodes, linkPreviewNode{Branch: branch, PRNumber: prs[branch], Unresolved: issues[branch]})
	}
	return preview
}

func (p linkPreview) commandText() string {
	parts := make([]string, len(p.Command))
	for index, argument := range p.Command {
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

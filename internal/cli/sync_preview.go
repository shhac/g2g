package cli

import (
	"fmt"
	"io"

	syncer "github.com/shhac/gt2gh/internal/sync"
)

func syncView(plan syncer.Plan) stackView {
	view := stackView{
		Operation:    "sync",
		Target:       plan.Discovery.Target,
		TargetSource: plan.Discovery.TargetSource,
		Nodes:        []stackNode{{Branch: plan.Discovery.Base, Trunk: true}},
	}
	for _, item := range plan.Items {
		node := stackNode{
			Branch:   item.Branch,
			Target:   item.Branch == plan.Discovery.Target,
			State:    syncDetail(item),
			Severity: syncSeverity(item.State),
		}
		if item.PullRequest != nil {
			node.PRNumber, node.PRURL = item.PullRequest.Number, item.PullRequest.URL
		}
		view.Nodes = append(view.Nodes, node)
	}

	// As in link, only an unconstructible command is withheld; a blocked one is
	// shown and labelled.
	if len(plan.Discovery.Branches) >= 2 {
		view.Action = append([]string{"gh", "stack", "link", "--base", plan.Discovery.Base}, plan.Discovery.Branches...)
	}
	if !plan.CanApply() {
		return view.block("Apply blocked: resolve every missing or non-open GitHub pull request first.")
	}
	if len(view.Action) == 0 {
		return view.note("Nothing to sync — this stack has one pull request.", severityNeutral)
	}
	return view
}

func syncDetail(item syncer.Item) string {
	switch item.State {
	case syncer.Aligned:
		return "aligned"
	case syncer.Divergent:
		return fmt.Sprintf("divergent: base %s, want %s", item.PullRequest.Base, item.ExpectedBase)
	case syncer.Missing:
		return "missing pull request"
	case syncer.Unsafe:
		return "non-open pull request"
	default:
		return string(item.State)
	}
}

func syncSeverity(state syncer.State) severity {
	switch state {
	case syncer.Aligned:
		return severityOK
	case syncer.Divergent:
		return severityWarn
	default:
		return severityBad
	}
}

func writeSyncPlan(writer io.Writer, plan syncer.Plan, presentation Presentation) error {
	return writeStackView(writer, syncView(plan), presentation)
}

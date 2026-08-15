package cli

import (
	"fmt"
	"io"

	syncer "github.com/shhac/gt2gh/internal/sync"
)

func syncView(plan syncer.Plan) stackView {
	view := stackView{
		Operation:    "sync",
		Target:       plan.Link.Target,
		TargetSource: plan.Link.TargetSource,
		Nodes:        []stackNode{{Branch: plan.Link.Base, Trunk: true}},
	}
	for _, item := range plan.Items {
		node := stackNode{
			Branch:   item.Branch,
			Target:   item.Branch == plan.Link.Target,
			State:    syncDetail(item),
			Severity: syncSeverity(item.State),
		}
		if item.PullRequest != nil {
			node.PRNumber, node.PRURL = item.PullRequest.Number, item.PullRequest.URL
		}
		view.Nodes = append(view.Nodes, node)
	}

	// As in link, the copyable command appears only when apply would accept it.
	if !plan.CanApply() {
		return view.note("Apply blocked: resolve every missing or non-open GitHub pull request first.", severityBad)
	}
	if plan.NothingToSync() {
		return view.note("Nothing to sync — this stack has one pull request.", severityNeutral)
	}
	view.Action = append([]string{"gh", "stack", "link", "--base", plan.Link.Base}, plan.Link.Branches...)
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

func writeReadyToSync(writer io.Writer, plan syncer.Plan, presentation Presentation) error {
	if err := writeReadyBanner(writer, presentation); err != nil {
		return err
	}
	return writeSyncPlan(writer, plan, presentation)
}

func writeSyncPlan(writer io.Writer, plan syncer.Plan, presentation Presentation) error {
	return writeStackView(writer, syncView(plan), presentation)
}

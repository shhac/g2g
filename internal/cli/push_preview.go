package cli

import (
	"io"

	"github.com/shhac/gt2gh/internal/push"
)

func pushView(plan push.Plan) stackView {
	view := stackView{
		Operation:    "push",
		Target:       plan.Target,
		TargetSource: plan.TargetSource,
		Nodes:        []stackNode{{Branch: plan.Base, Trunk: true}},
		Action:       append([]string{"git", "push", "--atomic", "--force-with-lease", plan.Remote}, plan.Branches...),
	}
	for _, branch := range plan.Branches {
		view.Nodes = append(view.Nodes, stackNode{Branch: branch, Target: branch == plan.Target})
	}
	return view.note("Atomic push: all selected refs advance together or none do.", severityNeutral)
}

func writeReadyToPush(writer io.Writer, plan push.Plan, presentation Presentation) error {
	if err := writeReadyBanner(writer, presentation); err != nil {
		return err
	}
	return writePushPlan(writer, plan, presentation)
}

func writePushPlan(writer io.Writer, plan push.Plan, presentation Presentation) error {
	return writeStackView(writer, pushView(plan), presentation)
}

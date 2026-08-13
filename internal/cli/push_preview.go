package cli

import "github.com/shhac/gt2gh/internal/push"

// pushPreview is the semantic terminal projection of an atomic Git push plan.
// It intentionally has no ANSI or writer dependency so another output format
// can consume its validated facts without scraping decorated terminal text.
type pushPreview struct {
	Target  string
	Nodes   []pushPreviewNode
	Command []string
}

type pushPreviewNode struct {
	Branch string
	Trunk  bool
}

func newPushPreview(plan push.Plan) pushPreview {
	preview := pushPreview{
		Target: plan.Target,
		Nodes:  []pushPreviewNode{{Branch: plan.Base, Trunk: true}},
		Command: append([]string{
			"git", "push", "--atomic", "--force-with-lease", plan.Remote,
		}, plan.Branches...),
	}
	for _, branch := range plan.Branches {
		preview.Nodes = append(preview.Nodes, pushPreviewNode{Branch: branch})
	}
	return preview
}

func (p pushPreview) commandText() string {
	return commandText(p.Command)
}

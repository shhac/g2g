package cli

import (
	"fmt"
	"io"
	"strings"

	syncer "github.com/shhac/gt2gh/internal/sync"
)

// syncPreview is the semantic terminal projection of a reconciliation plan.
// It contains no ANSI or writer behavior, so future formats need not scrape
// decorated terminal text.
type syncPreview struct {
	Target        string
	Nodes         []syncPreviewNode
	Command       []string
	ApplyBlocked  bool
	NothingToSync bool
}

type syncPreviewNode struct {
	Branch   string
	Trunk    bool
	PRNumber int
	State    syncer.State
	Detail   string
}

func newSyncPreview(plan syncer.Plan) syncPreview {
	preview := syncPreview{
		Target:        plan.Link.Target,
		Nodes:         []syncPreviewNode{{Branch: plan.Link.Base, Trunk: true}},
		ApplyBlocked:  !plan.CanApply(),
		NothingToSync: plan.NothingToSync(),
	}
	if !preview.NothingToSync {
		preview.Command = append([]string{"gh", "stack", "link", "--base", plan.Link.Base}, plan.Link.Branches...)
	}
	for _, item := range plan.Items {
		node := syncPreviewNode{Branch: item.Branch, State: item.State, Detail: syncDetail(item)}
		if item.PullRequest != nil {
			node.PRNumber = item.PullRequest.Number
		}
		preview.Nodes = append(preview.Nodes, node)
	}
	return preview
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

func (p syncPreview) commandText() string {
	return commandText(p.Command)
}

func writeReadyToSync(writer io.Writer, plan syncer.Plan, presentation Presentation) error {
	if _, err := fmt.Fprintln(writer, presentation.accent("Ready to apply")); err != nil {
		return err
	}
	return writeSyncPlan(writer, plan, presentation)
}

func writeSyncPlan(writer io.Writer, plan syncer.Plan, presentation Presentation) error {
	preview := newSyncPreview(plan)
	if _, err := fmt.Fprintf(writer, "%s: %s\n", presentation.accent("Target"), presentation.branch(preview.Target)); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(writer); err != nil {
		return err
	}
	for index, node := range preview.Nodes {
		if node.Trunk {
			if _, err := fmt.Fprintf(writer, "  %s\n", presentation.trunk(node.Branch+" (trunk)")); err != nil {
				return err
			}
			continue
		}
		label := presentation.branch(node.Branch)
		if node.PRNumber > 0 {
			label += " (" + presentation.pr(fmt.Sprintf("#%d", node.PRNumber)) + ")"
		}
		label += " " + syncStateText(presentation, node.State, node.Detail)
		if _, err := fmt.Fprintf(writer, "%s└─ %s\n", strings.Repeat("  ", index), label); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(writer); err != nil {
		return err
	}
	if preview.NothingToSync {
		if _, err := fmt.Fprintln(writer, "Nothing to sync — this stack has one pull request."); err != nil {
			return err
		}
	} else if err := writeCommand(writer, preview.commandText(), presentation); err != nil {
		return err
	}
	if preview.ApplyBlocked {
		if _, err := fmt.Fprintln(writer); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(writer, presentation.problem("Apply blocked: resolve every missing or non-open GitHub pull request first.")); err != nil {
			return err
		}
	}
	return nil
}

func syncStateText(presentation Presentation, state syncer.State, detail string) string {
	text := "(" + detail + ")"
	switch state {
	case syncer.Aligned:
		return presentation.aligned(text)
	case syncer.Divergent:
		return presentation.divergent(text)
	case syncer.Missing:
		return presentation.missing(text)
	case syncer.Unsafe:
		return presentation.unsafe(text)
	default:
		return presentation.problem(text)
	}
}

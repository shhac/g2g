package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/graph"
)

func rememberedPRs(ctx context.Context, view stackView, discovery graph.Discovery, reader githubstack.ObservationReader) stackView {
	if reader == nil {
		return view
	}
	observed, err := reader.Load(ctx)
	if err != nil {
		return view.note("Remembered PR state could not be read: "+err.Error()+" · run "+runnable("g2g github status")+" for a current check.", severityWarn)
	}
	shown := false
	for index, node := range view.Nodes {
		seen, known := observed[node.Branch]
		if !known {
			if discovery.States[node.Branch] == graph.StateBranchMissing {
				view.Nodes[index] = node.withMarks(stackMark{Detail: "PR history unknown", Severity: severityNeutral})
			}
			continue
		}
		shown = true
		node.PRNumber, node.PRURL = seen.PullRequest.Number, seen.PullRequest.URL
		detail := "last seen " + strings.ToLower(seen.PullRequest.State) + " " + seen.ObservedAt.UTC().Format(time.RFC3339)
		if seen.OpenCount > 1 {
			detail = fmt.Sprintf("%d PRs last seen open %s", seen.OpenCount, seen.ObservedAt.UTC().Format(time.RFC3339))
		}
		level := severityNeutral
		if discovery.States[node.Branch] == graph.StateBranchMissing && seen.PullRequest.State == "OPEN" {
			level = severityWarn
		}
		if !seen.MergeRequestedAt.IsZero() {
			detail += " · merge requested, confirmation pending"
		}
		view.Nodes[index] = node.withMarks(stackMark{Detail: "PR " + detail, Severity: level})
	}
	if shown {
		if discovery.Scope == graph.ScopeAll || !discovery.Graph.Tracked(discovery.Target) {
			view = view.note("PR state is remembered knowledge · run "+runnable("g2g github status --branch <branch>")+" for a current online check of a stack.", severityNeutral)
		} else {
			view = view.note("PR state is remembered knowledge · run "+runnable(selectedIn(discovery).next(githubStatusCommand))+" for a current online check.", severityNeutral)
		}
	}
	return view
}

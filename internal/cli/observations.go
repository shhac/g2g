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
		// A trunk is landed on, not opened as a pull request, so one seen with
		// a trunk's name as its head is somebody else's — a release from main
		// into another branch, say — and drawing it on the trunk read as the
		// trunk's own. A trunk declared to land somewhere is the exception:
		// that pull request is how it lands.
		if _, lands := discovery.Graph.Landing(node.Branch); node.Trunk && !lands {
			continue
		}
		seen, known := observed[node.Branch]
		if !known {
			if discovery.States[node.Branch] == graph.StateBranchMissing {
				view.Nodes[index] = node.withMarks(stackMark{Detail: "PR history unknown", Severity: severityNeutral})
			}
			continue
		}
		shown = true
		node.PRNumber, node.PRURL = seen.PullRequest.Number, seen.PullRequest.URL
		view.Nodes[index] = node.withMarks(rememberedMark(seen, discovery.States[node.Branch] == graph.StateBranchMissing))
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

// rememberedMark is what a branch's remembered pull request says about it. One
// still open on a branch that has gone is a warning: the pull request outlived
// the branch it was opened from.
func rememberedMark(seen githubstack.Observation, branchGone bool) stackMark {
	at := seen.ObservedAt.UTC().Format(time.RFC3339)
	detail := "last seen " + strings.ToLower(seen.PullRequest.State) + " " + at
	if seen.OpenCount > 1 {
		detail = fmt.Sprintf("%d PRs last seen open %s", seen.OpenCount, at)
	}
	if !seen.MergeRequestedAt.IsZero() {
		detail += " · merge requested, confirmation pending"
	}
	level := severityNeutral
	if branchGone && seen.PullRequest.State == "OPEN" {
		level = severityWarn
	}
	return stackMark{Detail: "PR " + detail, Severity: level}
}

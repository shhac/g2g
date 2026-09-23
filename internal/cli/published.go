package cli

import (
	"context"
	"fmt"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/push"
)

// Published says how each branch stands against what the remote last held,
// without a network: push.Known, over the refs a fetch or a push left behind.
type Published interface {
	Compare(ctx context.Context, remote string, branches []string, below func(string) (parent, trunk string)) (map[string]push.Publication, error)
}

// readPublished compares every local branch in the discovery with the remote.
//
// A repository with no such remote is ordinary — nothing has been published
// from it — so the default remote missing draws no marks rather than failing a
// read. One named on purpose is a mistake worth saying.
func readPublished(ctx context.Context, published Published, remote string, named bool, discovery graph.Discovery) (map[string]push.Publication, error) {
	if published == nil {
		return nil, nil
	}
	local := make([]string, 0, len(discovery.Branches))
	for _, branch := range discovery.Branches {
		if discovery.States[branch] != graph.StateBranchMissing {
			local = append(local, branch)
		}
	}
	publishing, err := published.Compare(ctx, remote, local, func(branch string) (string, string) {
		trunk := branch
		if path, err := discovery.Graph.Path(branch); err == nil && len(path) != 0 {
			trunk = path[0]
		}
		if parent, tracked := discovery.Graph.Parent(branch); tracked {
			return parent, trunk
		}
		return branch, trunk
	})
	if err != nil && !named {
		return nil, nil
	}
	return publishing, err
}

// publishedMark is one branch against its remote, in the words git status uses.
// A branch that landed and left the remote says nothing: its state already
// says what happened to it.
func publishedMark(remote string, publication push.Publication) stackMark {
	switch {
	case publication.Landed:
		return stackMark{}
	case publication.New:
		return stackMark{Detail: "not on " + remote, Severity: severityNeutral}
	case publication.Unknown:
		return stackMark{Subject: remote, Detail: "on a commit not here", Severity: severityWarn}
	case publication.Theirs > 0 && publication.Ours > 0:
		return stackMark{Subject: remote, Detail: fmt.Sprintf("diverged · %d here, %d there", publication.Ours, publication.Theirs), Severity: severityBad}
	case publication.Theirs > 0:
		return stackMark{Subject: remote, Detail: fmt.Sprintf("%d behind", publication.Theirs), Severity: severityWarn}
	case publication.Rewritten:
		return stackMark{Subject: remote, Detail: "replayed since pushed", Severity: severityWarn}
	case publication.Ours > 0:
		return stackMark{Subject: remote, Detail: fmt.Sprintf("%d ahead", publication.Ours), Severity: severityWarn}
	default:
		return stackMark{Subject: remote, OK: true, Severity: severityOK}
	}
}

// markPublished adds each compared branch's remote mark beside what the graph
// already says of it. A branch that was not compared says nothing, rather
// than reading as up to date.
func markPublished(view stackView, remote string, publishing map[string]push.Publication) stackView {
	if publishing == nil {
		return view
	}
	for index, node := range view.Nodes {
		publication, compared := publishing[node.Branch]
		if !compared {
			continue
		}
		// Being a trunk is said by the line itself, so a trunk's own mark is
		// only how it stands against the remote.
		said := stackMark{Detail: node.State, Severity: node.Severity}
		if node.Trunk {
			said = stackMark{}
		}
		view.Nodes[index] = node.marked(said, publishedMark(remote, publication))
	}
	return publishedNotes(view, remote, publishing)
}

// publishedNotes are the next steps, as git status gives them: what to run for
// the branches that are not where the remote is.
func publishedNotes(view stackView, remote string, publishing map[string]push.Publication) stackView {
	var ahead, behind, diverged, unknown []string
	for _, node := range view.Nodes {
		publication, compared := publishing[node.Branch]
		switch {
		case !compared || publication.Landed:
		case publication.Unknown:
			unknown = append(unknown, node.Branch)
		case publication.Theirs > 0 && publication.Ours > 0:
			diverged = append(diverged, node.Branch)
		case publication.Theirs > 0:
			behind = append(behind, node.Branch)
		case node.Trunk:
			// A trunk is published by landing on it, never by pushing it.
		case publication.New || publication.Rewritten || publication.Ours > 0:
			ahead = append(ahead, node.Branch)
		}
	}
	if len(ahead) != 0 {
		view = view.note("Not on "+remote+" as they are here: "+branchList(ahead)+" · run "+runnable("g2g push")+".", severityWarn)
	}
	if len(behind) != 0 {
		view = view.note(remote+" has work "+branchList(behind)+" "+pick(len(behind), "does", "do")+" not · run "+runnable("g2g pull")+".", severityWarn)
	}
	if len(diverged) != 0 {
		view = view.note("Diverged from "+remote+": "+branchList(diverged)+" · run "+runnable("g2g pull")+" to see the ways to reconcile.", severityBad)
	}
	if len(unknown) != 0 {
		view = view.note(remote+" is on a commit this repository has not fetched for "+branchList(unknown)+" · run "+runnable("g2g pull")+" to see it.", severityWarn)
	}
	return view.note("Compared with "+remote+" as last fetched or pushed · nothing was asked of the network.", severityNeutral)
}

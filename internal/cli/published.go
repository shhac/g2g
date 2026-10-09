package cli

import (
	"context"
	"errors"
	"fmt"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/push"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/shape"
)

// readPublished compares every local branch in the discovery with the remote.
//
// A repository with no such remote is ordinary — nothing has been published
// from it — so the default remote missing draws no marks rather than failing a
// read. One named on purpose is a mistake worth saying, and so is any other
// failure: drawing no marks for one would read as nothing to report, and let
// doctor say a repository is healthy when it could not tell.
func readPublished(ctx context.Context, published push.Known, remote string, named bool, discovery graph.Discovery) (map[string]push.Publication, error) {
	if !published.Ready() {
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
	if errors.Is(err, localgit.ErrNoSuchRemote) && !named {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return publishing, carried(ctx, published, remote, discovery, publishing)
}

// carried marks a branch the remote is ahead of only because it was moved
// back by hand past commits a branch stacked on it still carries. Advising a
// pull there would put those commits back on it and undo the move; what the
// person has to decide is whether the child keeps them, which is restack's
// --absorb.
func carried(ctx context.Context, published push.Known, remote string, discovery graph.Discovery, publishing map[string]push.Publication) error {
	behind := make([]string, 0)
	for branch, publication := range publishing {
		if publication.Standing == push.Behind {
			behind = append(behind, branch)
		}
	}
	if len(behind) == 0 {
		return nil
	}
	tips, err := published.Git.KnownTips(ctx, remote, behind)
	if err != nil {
		return err
	}
	for _, branch := range behind {
		publication := publishing[branch]
		for _, child := range discovery.Graph.Children(branch) {
			if discovery.States[child] == graph.StateBranchMissing {
				continue
			}
			holds, err := published.Git.IsAncestor(ctx, tips[branch], child)
			if err != nil {
				return err
			}
			if holds {
				publication.CarriedBy = child
				publishing[branch] = publication
				break
			}
		}
	}
	return nil
}

// publishedMark is one branch against its remote, in the words git status uses.
// A branch that landed and left the remote says nothing: its state already
// says what happened to it.
func publishedMark(remote string, publication push.Publication) stackMark {
	switch publication.Standing {
	case push.New:
		return stackMark{Detail: "not on " + remote, Severity: severityNeutral}
	case push.Unknown:
		return stackMark{Subject: remote, Detail: "on a commit not here", Severity: severityWarn}
	case push.Diverged:
		return stackMark{Subject: remote, Detail: "diverged · " + eachSide(publication), Severity: severityBad}
	case push.Behind:
		return stackMark{Subject: remote, Detail: fmt.Sprintf("%d behind", publication.Theirs), Severity: severityWarn}
	case push.Rewritten:
		return stackMark{Subject: remote, Detail: "replayed since pushed", Severity: severityWarn}
	case push.Ahead:
		return stackMark{Subject: remote, Detail: fmt.Sprintf("%d ahead", publication.Ours), Severity: severityWarn}
	case push.Current:
		return stackMark{Subject: remote, OK: true, Severity: severityOK}
	default:
		return stackMark{}
	}
}

// eachSide is how many commits each side of a divergence has, which is what
// deciding between them needs.
func eachSide(publication push.Publication) string {
	return fmt.Sprintf("%d here, %d there", publication.Ours, publication.Theirs)
}

// markPublished adds each compared branch's remote mark beside what the graph
// already says of it. A branch that was not compared says nothing, rather
// than reading as up to date. acted is the selection and the remote it was
// compared with, which the next steps are aimed at.
func markPublished(view stackView, acted selected, publishing map[string]push.Publication) stackView {
	remote := acted.remote
	if publishing == nil {
		return view
	}
	for index, node := range view.Nodes {
		publication := publishing[node.Branch]
		if publication.Standing == push.Uncompared {
			continue
		}
		// Being a trunk is said by the line itself, so a trunk's own mark is
		// only how it stands against the remote.
		mark := publishedMark(remote, publication)
		if node.Trunk {
			view.Nodes[index] = node.marked(mark)
		} else {
			view.Nodes[index] = node.withMarks(mark)
		}
	}
	return publishedNotes(view, acted, publishing)
}

// publishedNotes are the next steps, as git status gives them: what to run for
// the branches that are not where the remote is.
func publishedNotes(view stackView, acted selected, publishing map[string]push.Publication) stackView {
	remote := acted.remote
	var ahead, behind, diverged, unknown []string
	for _, node := range view.Nodes {
		publication := publishing[node.Branch]
		switch {
		case publication.Standing == push.Unknown:
			unknown = append(unknown, node.Branch)
		case publication.Standing == push.Diverged:
			diverged = append(diverged, node.Branch)
		case publication.CarriedBy != "":
			view = view.note(fmt.Sprintf("%s was moved back past %s %s still carries · run %s to keep them in %s, or %s to drop them from it too.",
				node.Branch, count(publication.Theirs, "commit", "commits"), publication.CarriedBy,
				runnable("g2g restack --branch "+repair.Quote(publication.CarriedBy)+" --absorb"), publication.CarriedBy,
				runnable("g2g restack --branch "+repair.Quote(publication.CarriedBy))), severityWarn)
		case publication.Standing == push.Behind:
			behind = append(behind, node.Branch)
		// A trunk is published by landing on it, never by pushing it.
		case publication.Unpublished() && !node.Trunk:
			ahead = append(ahead, node.Branch)
		}
	}
	if len(ahead) != 0 {
		view = view.note("Not on "+remote+" as they are here: "+branchList(ahead)+" · run "+runnable(pushFor(view, acted, ahead))+".", severityWarn)
	}
	if len(behind) != 0 {
		view = view.note(remote+" has work "+branchList(behind)+" "+pick(len(behind), "does", "do")+" not · run "+runnable(acted.aimedOr(pullCommand))+".", severityWarn)
	}
	if len(diverged) != 0 {
		view = view.note("Diverged from "+remote+": "+branchList(diverged)+" · run "+runnable(acted.aimedOr(pullCommand))+" to see the ways to reconcile.", severityBad)
	}
	if len(unknown) != 0 {
		view = view.note(remote+" is on a commit this repository has not fetched for "+branchList(unknown)+" · run "+runnable(acted.aimedOr(pullCommand))+" to see it.", severityWarn)
	}
	return view.note("Compared with "+remote+" as last fetched or pushed · nothing was asked of the network.", severityNeutral)
}

// pushFor is push aimed at the branches that are not published as they are
// here, cut down to the narrowest scope around the target that still covers
// them: the target alone, the path below it, everything above it, or both. A scope
// is only named when it selects less than status showed, since otherwise the
// push it names is the one status's own selection already suggests. Reaching
// less matters beyond reading as bounded: one branch the remote is ahead on
// refuses the whole push, so a push that does not reach it is not refused
// for it.
func pushFor(view stackView, acted selected, unpublished []string) string {
	parents := make(map[string]string, len(view.Nodes))
	shown := 0
	for _, node := range view.Nodes {
		parents[node.Branch] = node.Parent
		if node.Branch == acted.branch && node.Trunk {
			return acted.aimedOr(pushCommand)
		}
		if !node.Trunk {
			shown++
		}
	}
	if _, present := parents[acted.branch]; !present {
		return acted.aimedOr(pushCommand)
	}
	for _, scope := range []shape.Scope{shape.ScopeBranch, shape.ScopePath, shape.ScopeSubtree, shape.ScopeStack} {
		covered := scopedFrom(view, parents, acted.branch, scope)
		if len(covered) < shown && coversAll(covered, unpublished) {
			return acted.narrowedTo(scope).aimedOr(pushCommand)
		}
	}
	return acted.aimedOr(pushCommand)
}

// scopedFrom is the branches a scope around target selects among those shown,
// leaving out trunks, which a push never publishes.
func scopedFrom(view stackView, parents map[string]string, target string, scope shape.Scope) map[string]bool {
	covered := make(map[string]bool, len(view.Nodes))
	for _, node := range view.Nodes {
		if node.Trunk {
			continue
		}
		var within bool
		switch scope {
		case shape.ScopeBranch:
			within = node.Branch == target
		case shape.ScopePath:
			within = descends(parents, target, node.Branch)
		case shape.ScopeSubtree:
			within = descends(parents, node.Branch, target)
		case shape.ScopeStack:
			within = descends(parents, target, node.Branch) || descends(parents, node.Branch, target)
		}
		if within {
			covered[node.Branch] = true
		}
	}
	return covered
}

// descends reports whether branch is ancestor itself or sits somewhere above
// it, following the parents shown. The walk is bounded by how many there are,
// so a record that loops cannot hold it.
func descends(parents map[string]string, branch, ancestor string) bool {
	for step := 0; step <= len(parents) && branch != ""; step++ {
		if branch == ancestor {
			return true
		}
		branch = parents[branch]
	}
	return false
}

func coversAll(covered map[string]bool, branches []string) bool {
	for _, branch := range branches {
		if !covered[branch] {
			return false
		}
	}
	return true
}

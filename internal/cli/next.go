package cli

import (
	"slices"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/restack"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
)

// defaultRemote is the remote a command reads and publishes to when none is
// named, and so the one a suggestion need not name.
const defaultRemote = "origin"

// replayNext follows a rewrite, pull's or restack's, from what it did. A
// branch whose work it found already in its base is forgotten first, because
// publishing would push a branch that has landed; prune also records what sat
// on it where it now sits. Otherwise a replay leaves the published branches
// behind their local ones, and push previews what publishing them would do. A
// rewrite that replayed nothing leaves nothing to follow.
func replayNext(plan restack.Plan) string {
	acted := selectedIn(plan.Discovery)
	switch {
	case len(plan.Emptied()) != 0:
		return acted.next("g2g prune", shape.ReadScopes, graph.ScopeStack)
	case len(plan.Replaying()) != 0:
		return acted.next("g2g push", shape.ProjectScopes, shape.ScopeStack)
	default:
		return ""
	}
}

// selected is what a command acted on: the branch, whether it was named, and
// how much around it. A suggestion aims at it, so that running the suggestion
// reaches what the command touched rather than wherever the reader stands.
type selected struct {
	branch string
	named  bool
	scope  shape.Scope
}

func selectedIn(discovery graph.Discovery) selected {
	return selected{branch: discovery.Target, named: discovery.TargetSource == shape.TargetNamed, scope: discovery.Scope}
}

func selectedFrom(snapshot stack.Snapshot) selected {
	return selected{branch: snapshot.Target, named: snapshot.TargetSource == shape.TargetNamed, scope: snapshot.Scope}
}

// aim is command pointed at the selection. It names the branch only when the
// branch was named, since otherwise the reader is standing on it, and the scope
// only when the command's default would not reach everything selected. It
// fails when the command offers no scope that would.
func (s selected) aim(command string, offered []shape.Scope, fallback shape.Scope) (string, bool) {
	if s.named {
		command += " --branch " + s.branch
	}
	switch {
	case s.scope == "" || s.scope.Within(fallback):
		return command, true
	case slices.Contains(offered, s.scope):
		return command + " --scope " + string(s.scope), true
	default:
		return "", false
	}
}

// next is command aimed at the selection, or status over the same selection
// when the command cannot reach all of it: a trunk restacked whole has
// replayed several stacks, and push takes one at a time.
func (s selected) next(command string, offered []shape.Scope, fallback shape.Scope) string {
	if aimed, ok := s.aim(command, offered, fallback); ok {
		return aimed
	}
	status, _ := s.aim("g2g status", shape.ReadScopes, graph.ScopeStack)
	return status
}

// statusNext is status over what a command recorded.
func statusNext(discovery graph.Discovery) string {
	return selectedIn(discovery).next("g2g status", shape.ReadScopes, graph.ScopeStack)
}

// githubStatusNext is github status over what a command did on GitHub.
func githubStatusNext(snapshot stack.Snapshot) string {
	return selectedFrom(snapshot).next("g2g github status", shape.Scopes, shape.ScopeStack)
}

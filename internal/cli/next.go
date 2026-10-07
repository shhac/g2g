package cli

import (
	"slices"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/restack"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
)

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
		return acted.next(pruneCommand)
	case len(plan.Replaying()) != 0:
		return acted.next(pushCommand)
	default:
		return ""
	}
}

// suggestable is a command a suggestion can name, with the --scope values it
// accepts and what it selects without one. The command registers its flag
// from the same value, so a suggestion can neither offer a scope the command
// refuses nor leave off one its default would not reach.
type suggestable struct {
	command  string
	accepted []shape.Scope
	fallback shape.Scope
}

var (
	statusCommand       = suggestable{command: "g2g status", accepted: shape.ReadScopes, fallback: shape.ScopeStack}
	pruneCommand        = suggestable{command: "g2g prune", accepted: shape.ReadScopes, fallback: shape.ScopeStack}
	pushCommand         = suggestable{command: "g2g push", accepted: shape.ProjectScopes, fallback: shape.ScopeStack}
	githubStatusCommand = suggestable{command: "g2g github status", accepted: shape.Scopes, fallback: shape.ScopeStack}
)

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

// aim is target pointed at the selection. It names the branch only when the
// branch was named, since otherwise the reader is standing on it, and the scope
// only when the target's default would not reach everything selected. It
// fails when the target accepts no scope that would.
func (s selected) aim(target suggestable) (string, bool) {
	command := target.command
	if s.named {
		command += " --branch " + s.branch
	}
	switch {
	case s.scope == "" || s.scope.Within(target.fallback):
		return command, true
	case slices.Contains(target.accepted, s.scope):
		return command + " --scope " + string(s.scope), true
	default:
		return "", false
	}
}

// next is command aimed at the selection, or status over the same selection
// when the command cannot reach all of it: a trunk restacked whole has
// replayed several stacks, and push takes one at a time.
func (s selected) next(target suggestable) string {
	if aimed, ok := s.aim(target); ok {
		return aimed
	}
	status, _ := s.aim(statusCommand)
	return status
}

// statusNext is status over what a command recorded.
func statusNext(discovery graph.Discovery) string {
	return selectedIn(discovery).next(statusCommand)
}

// githubStatusNext is github status over what a command did on GitHub.
func githubStatusNext(snapshot stack.Snapshot) string {
	return selectedFrom(snapshot).next(githubStatusCommand)
}

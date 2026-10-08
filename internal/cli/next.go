package cli

import (
	"slices"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/restack"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
)

// replayNext follows a rewrite, pull's or restack's, from what it did. A
// branch whose work it found already in its base is forgotten first, because
// publishing would push a branch that has landed; prune also records what sat
// on it where it now sits. Otherwise a replay leaves the published branches
// behind their local ones, and push previews what publishing them would do. A
// rewrite that replayed nothing leaves nothing to follow. remote is where the
// rewrite read published branches from, empty for one that read none.
func replayNext(plan restack.Plan, remote string) string {
	acted := selectedIn(plan.Discovery).from(remote)
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
// accepts and what it selects without one, and whether it takes --remote. The
// command registers its flag from the same value, so a suggestion can neither
// offer a scope the command refuses nor leave off one its default would not
// reach.
type suggestable struct {
	command  string
	accepted []shape.Scope
	fallback shape.Scope
	remote   bool
}

var (
	statusCommand       = suggestable{command: "g2g status", accepted: shape.ReadScopes, fallback: shape.ScopeStack, remote: true}
	pruneCommand        = suggestable{command: "g2g prune", accepted: shape.ReadScopes, fallback: shape.ScopeStack}
	pushCommand         = suggestable{command: "g2g push", accepted: shape.Scopes, fallback: shape.ScopeStack, remote: true}
	pullCommand         = suggestable{command: "g2g pull", accepted: shape.SyncScopes, fallback: shape.ScopeStack, remote: true}
	githubStatusCommand = suggestable{command: "g2g github status", accepted: shape.Scopes, fallback: shape.ScopeStack}
	retargetCommand     = suggestable{command: "g2g github retarget", accepted: shape.ProjectScopes, fallback: shape.ScopeStack}
	// commentCommand takes no scope: it keeps the whole stack a branch is in.
	commentCommand = suggestable{command: "g2g github comment"}
)

// selected is what a command acted on: the branch, whether it was named, how
// much around it, and the remote it compared with. A suggestion aims at it, so
// that running the suggestion reaches what the command touched rather than
// wherever the reader stands, and compares with the same remote: --remote
// names where a stack's branches are published, for pull and push alike.
type selected struct {
	branch string
	named  bool
	scope  shape.Scope
	remote string
}

func selectedIn(discovery graph.Discovery) selected {
	return selected{branch: discovery.Target, named: discovery.TargetSource == shape.TargetNamed, scope: discovery.Scope}
}

func selectedFrom(snapshot stack.Snapshot) selected {
	return selected{branch: snapshot.Target, named: snapshot.TargetSource == shape.TargetNamed, scope: snapshot.Scope}
}

// from is the selection as compared with remote.
func (s selected) from(remote string) selected {
	s.remote = remote
	return s
}

// aim is target pointed at the selection. It names the branch only when the
// branch was named, since otherwise the reader is standing on it, the scope
// only when the target's default would not reach everything selected, and the
// remote only when it is not the default and the target takes one. It fails
// when the target accepts no scope that would reach the selection.
func (s selected) aim(target suggestable) (string, bool) {
	command := target.command
	if s.named {
		command += " --branch " + repair.Quote(s.branch)
	}
	switch {
	case s.scope == "" || s.scope.Within(target.fallback):
	case slices.Contains(target.accepted, s.scope):
		command += " --scope " + string(s.scope)
	default:
		return "", false
	}
	if target.remote && s.remote != "" && s.remote != localgit.DefaultRemote {
		command += " --remote " + repair.Quote(s.remote)
	}
	return command, true
}

// next is command aimed at the selection, or status over the same selection
// when the command cannot reach all of it: every trunk's stacks are wider than
// anything but a read takes.
func (s selected) next(target suggestable) string {
	if aimed, ok := s.aim(target); ok {
		return aimed
	}
	status, _ := s.aim(statusCommand)
	return status
}

// aimedOr is target aimed at the selection, or the bare command when no scope
// it accepts reaches all of it. It is for advice inside a status view, where
// falling back to status would send the reader to where they already are.
func (s selected) aimedOr(target suggestable) string {
	if aimed, ok := s.aim(target); ok {
		return aimed
	}
	return target.command
}

// statusNext is status over what a command recorded.
func statusNext(discovery graph.Discovery) string {
	return selectedIn(discovery).next(statusCommand)
}

// githubStatusNext is github status over what a command did on GitHub.
func githubStatusNext(snapshot stack.Snapshot) string {
	return selectedFrom(snapshot).next(githubStatusCommand)
}

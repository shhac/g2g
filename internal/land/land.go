// Package land takes a stack down onto its trunk, one branch at a time.
//
// It owns no rules of its own. Publishing, advancing the base, replaying and
// forgetting are each a service that already exists, already previews, and
// already refuses the things it knows how to refuse; this decides only the
// order, waits for GitHub in the two places where acting on a stale answer
// would merge the wrong thing, and rewrites one edge per branch that lands.
package land

import (
	"context"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/prune"
	"github.com/shhac/g2g/internal/push"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/stack"
	syncer "github.com/shhac/g2g/internal/sync"
)

// Git is the local half: what is here, what the remote has, and the two ref
// deletions landing performs.
type Git interface {
	stack.Git
	Clean(ctx context.Context) error
	Resolve(ctx context.Context, revision string) (string, error)
	IsAncestor(ctx context.Context, ancestor, descendant string) (bool, error)
	RemoteTips(ctx context.Context, remote string, branches []string) (map[string]string, error)
	FetchIsolated(ctx context.Context, remote string, branches []string) error
	Cherry(ctx context.Context, upstream, head, limit string) (absent, present []string, err error)
	Absorbed(ctx context.Context, base, branch string) (bool, error)
	DeleteBranch(ctx context.Context, branch string) error
	DeleteRemoteBranch(ctx context.Context, remote, branch string) error
	SwitchBranch(ctx context.Context, branch string) error
	SwitchDetached(ctx context.Context, revision string) error
}

// GitHub is what landing asks of gh: who the pull requests are, what their
// merges would do, and the two mutations that land one.
type GitHub interface {
	Inspect(ctx context.Context, branches []string) ([]githubstack.PullRequest, error)
	Mergeability(ctx context.Context, numbers []int) (githubstack.Mergeability, error)
	Merge(ctx context.Context, number int, method githubstack.Method, admin bool, head string) error
	Retarget(ctx context.Context, number int, base string) error
}

// The three services landing composes. Each is an interface rather than the
// service itself for the reason sync gives for the same choice: the job here is
// ordering, and ordering should be testable without standing up a rewrite
// engine or a network.
type (
	// Pusher publishes one branch and refuses a remote that has moved.
	Pusher interface {
		Plan(ctx context.Context, selection stack.Selection, remote string, upstream localgit.Upstream) (push.Plan, error)
		Execute(ctx context.Context, plan push.Plan) error
	}
	// Syncer advances the base and replays what is left onto it.
	Syncer interface {
		Plan(ctx context.Context, selection graph.Selection, remote string, take syncer.Take) (syncer.Plan, error)
		Apply(ctx context.Context, plan syncer.Plan) error
	}
	// Pruner forgets a branch whose work Git says is upstream.
	Pruner interface {
		Plan(ctx context.Context, selection graph.Selection) (prune.Plan, error)
		Apply(ctx context.Context, plan prune.Plan) error
	}
	// Holds says which of these branches another worktree has checked out.
	// It is restack's own check, asked here of everything a descent will move.
	Holds interface {
		HeldElsewhere(ctx context.Context, branches []string) (repair.Note, error)
	}
)

// Service takes a stack down onto its trunk.
type Service struct {
	Git      Git
	Graph    graph.Service
	Selector stack.PathSelector
	GitHub   GitHub
	Pusher   Pusher
	Syncer   Syncer
	Pruner   Pruner
	// Holds is optional: a build that cannot ask lands exactly as safely as it
	// did before the check existed.
	Holds Holds

	// pause is the clock the two waits use. Nil is the real one; a test
	// supplies its own so the suite does not spend the wall time.
	pause pauser
}

// Options are the choices a run was given.
type Options struct {
	Remote string
	Method githubstack.Method
	// MethodChosen means the run named the method. Without it, a declared trunk
	// lands the way its declaration says.
	MethodChosen bool
	Admin        bool
	// The two deletions, both on unless asked otherwise. Forgetting each
	// landed branch is not an option: one left recorded under a branch that
	// has merged and gone makes every later replay measure against a structure
	// that is not there.
	DeleteRemote bool
	DeleteLocal  bool
	// Upstream is whether each branch the descent publishes is recorded as
	// tracking the remote's copy, as push does by default.
	Upstream localgit.Upstream
	// Comment keeps the stack comments on what remains above the landed
	// branches once the descent is done, so the pull requests that merged
	// read as merged history there. On unless asked otherwise.
	Comment bool
}

// Defaults are the options a bare invocation means.
func Defaults() Options {
	return Options{Remote: localgit.DefaultRemote, Method: githubstack.MethodSquash, DeleteRemote: true, DeleteLocal: true, Comment: true}
}

// Ready reports a service with everything it needs.
//
// One rule, called by both the guard below and the command registration in
// internal/cli, which spelled the same six-way conjunction out by hand.
func (s Service) Ready() bool {
	return s.Git != nil && s.Selector != nil && s.GitHub != nil &&
		s.Pusher != nil && s.Syncer != nil && s.Pruner != nil
}

var _ Git = localgit.Client{}

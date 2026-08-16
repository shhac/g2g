// Package sync brings a stack up to date with its remote: fetch, advance the
// base, replay, and forget what has landed.
//
// It is an orchestrator and owns no rules of its own. Each step is a service
// that already exists and is already previewable, so what this adds is the
// order and the honesty about how far it got.
package sync

import (
	"context"
	"fmt"
	"strings"

	"github.com/shhac/gt2gh/internal/diagnostic"
	localgit "github.com/shhac/gt2gh/internal/git"
	"github.com/shhac/gt2gh/internal/graph"
	"github.com/shhac/gt2gh/internal/restack"
)

// Git is the boundary for the steps sync performs itself.
type Git interface {
	RemoteTips(ctx context.Context, remote string, branches []string) (map[string]string, error)
	FetchIsolated(ctx context.Context, remote string, branches []string) error
	FastForward(ctx context.Context, branch, to string) error
	Resolve(ctx context.Context, revision string) (string, error)
	IsAncestor(ctx context.Context, ancestor, descendant string) (bool, error)
	Remote(ctx context.Context, name string) error
}

// Restacker is the replay step. It is an interface rather than the service
// itself because sync's own job is ordering, and ordering should be testable
// without standing up a rewrite engine.
type Restacker interface {
	Plan(ctx context.Context, selection graph.Selection, onto string, absorb bool) (restack.Plan, error)
	Apply(ctx context.Context, plan restack.Plan) error
	InProgress(ctx context.Context) (bool, error)
}

// Service composes the steps. Each is separately testable and separately
// previewable; this decides only what runs, in what order, and when to stop.
type Service struct {
	Git     Git
	Graph   graph.Service
	Restack Restacker
}

// Plan is what a sync would do, in the order it would do it.
type Plan struct {
	Restack restack.Plan
	Remote  string
	// Base is the branch the selection is rooted on, and Advance reports that
	// the remote has moved past where it currently is.
	Base    string
	Advance bool
	// Diverged means the base cannot be fast-forwarded. Nothing is attempted
	// in that case: reconciling it is the user's call, not a side effect.
	Diverged bool
	// Prunable is the branches whose work is entirely in their parent, which
	// is what a landed branch looks like after its replay.
	Prunable []string
	// Blocked is why an apply would refuse, empty when it would proceed.
	Blocked string
}

// Plan works out the whole sequence without performing any of it. The fetch is
// the one step that reaches the network, and it writes only into gt2gh's own
// ref namespace, so previewing costs the repository nothing.
func (s Service) Plan(ctx context.Context, selection graph.Selection, remote string) (Plan, error) {
	if s.Git == nil || s.Restack == nil {
		return Plan{}, fmt.Errorf("sync service is not fully configured")
	}
	if err := s.Git.Remote(ctx, remote); err != nil {
		return Plan{}, err
	}
	discovery, err := s.Graph.Discover(ctx, graph.Selection{Branch: selection.Branch, Scope: graph.ScopeGraph})
	if err != nil {
		return Plan{}, err
	}
	// A selection of one is the branch itself with nothing recorded under it,
	// so there is no base to bring up to date.
	if len(discovery.Branches) < 2 {
		return Plan{}, fmt.Errorf("%q has no recorded parent to sync against · run g2g track to record one", discovery.Target)
	}
	plan := Plan{Remote: remote, Base: discovery.Branches[0]}

	if err := s.Git.FetchIsolated(ctx, remote, []string{plan.Base}); err != nil {
		return Plan{}, err
	}
	plan.Advance, plan.Diverged, err = s.compare(ctx, plan.Base, remote)
	if err != nil {
		return Plan{}, err
	}
	if plan.Diverged {
		plan.Blocked = fmt.Sprintf("%s has diverged from %s/%s · reconcile it yourself, then rerun", plan.Base, remote, plan.Base)
		return plan, nil
	}

	// The replay is planned against the base as it will be, which is why the
	// fetch and the fast-forward assessment come first.
	plan.Restack, err = s.Restack.Plan(ctx, selection, s.onto(plan), false)
	if err != nil {
		return Plan{}, err
	}
	plan.Blocked = plan.Restack.Blocked
	plan.Prunable = prunable(plan.Restack)
	diagnostic.Event(ctx, "sync.plan",
		diagnostic.Field{Key: "base", Value: plan.Base},
		diagnostic.Field{Key: "advance", Value: fmt.Sprintf("%t", plan.Advance)},
		diagnostic.Field{Key: "replays", Value: strings.Join(plan.Restack.Replaying(), ",")},
		diagnostic.Field{Key: "prunable", Value: strings.Join(plan.Prunable, ",")},
	)
	return plan, nil
}

// onto names the base the replay should land on. Until the base branch is
// advanced it is still where it was, so the fetched ref is what the replay
// has to target.
func (s Service) onto(plan Plan) string {
	if !plan.Advance {
		return ""
	}
	return localgit.IsolatedRef(plan.Remote, plan.Base)
}

// compare asks how the base stands against the remote without changing it.
func (s Service) compare(ctx context.Context, base, remote string) (advance, diverged bool, err error) {
	fetched := localgit.IsolatedRef(remote, base)
	local, err := s.Git.Resolve(ctx, base)
	if err != nil {
		return false, false, err
	}
	upstream, err := s.Git.Resolve(ctx, fetched)
	if err != nil {
		// The base is not on the remote at all, which is ordinary for a local
		// trunk that was never pushed.
		return false, false, nil
	}
	if local == upstream {
		return false, false, nil
	}
	behind, err := s.Git.IsAncestor(ctx, base, fetched)
	if err != nil {
		return false, false, err
	}
	return behind, !behind, nil
}

// prunable is the branches a replay leaves with nothing of their own, which is
// what a landed branch looks like once its work is in its parent.
func prunable(plan restack.Plan) []string {
	return append([]string(nil), plan.Emptied()...)
}

// Apply performs the sequence and stops at the first step that cannot finish.
//
// It reports how far it got rather than unwinding: a replay that stops on a
// conflict is resumable, and undoing the fetch and the fast-forward would
// throw away work the user then has to redo.
func (s Service) Apply(ctx context.Context, plan Plan, prune bool) error {
	if plan.Blocked != "" {
		return fmt.Errorf("cannot sync: %s", plan.Blocked)
	}
	if plan.Advance {
		diagnostic.Event(ctx, "sync.advance", diagnostic.Field{Key: "base", Value: plan.Base})
		if err := s.Git.FastForward(ctx, plan.Base, localgit.IsolatedRef(plan.Remote, plan.Base)); err != nil {
			return err
		}
	}
	if len(plan.Restack.Steps) != 0 {
		if err := s.Restack.Apply(ctx, plan.Restack); err != nil {
			return err
		}
	}
	if !prune || len(plan.Prunable) == 0 {
		return nil
	}
	return s.prune(ctx, plan.Prunable)
}

// prune forgets branches whose work has landed. It edits the recorded graph
// and never deletes a branch: removing someone's local work is not something
// to do as the tail of another command.
func (s Service) prune(ctx context.Context, branches []string) error {
	adopted, err := s.Graph.Store.Load(ctx)
	if err != nil {
		return err
	}
	diagnostic.Event(ctx, "sync.prune", diagnostic.Field{Key: "branches", Value: strings.Join(branches, ",")})
	updated := adopted.Untrack(branches...)
	if err := s.Graph.Store.Save(ctx, updated); err != nil {
		return err
	}
	if s.Graph.Refs == nil {
		return nil
	}
	for _, branch := range branches {
		if err := s.Graph.Refs.UnpinForkPoint(ctx, branch); err != nil {
			return err
		}
	}
	return nil
}

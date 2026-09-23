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
	"slices"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/restack"
)

// Git is the boundary for the steps sync performs itself.
type Git interface {
	FetchIsolated(ctx context.Context, remote string, branches []string) error
	FastForward(ctx context.Context, branch, to string) error
	ResetBranch(ctx context.Context, branch, to string) error
	Resolve(ctx context.Context, revision string) (string, error)
	IsAncestor(ctx context.Context, ancestor, descendant string) (bool, error)
	Remote(ctx context.Context, name string) error
	// RemoteTips says which of these branches the remote still has. A fetch
	// naming a ref the remote deleted fails outright, and a branch being gone
	// because it merged is the ordinary case, not an error.
	RemoteTips(ctx context.Context, remote string, branches []string) (map[string]string, error)
	// Cherry answers whether the commits on one side are present on the other
	// by content, which is how a branch somebody else rebased is recognised as
	// still being your work rather than as a divergence.
	Cherry(ctx context.Context, upstream, head, limit string) (absent, present []string, err error)
	// Absorbed answers the same of a whole branch at once, which is the only
	// way to see that a published version's commits are here after its parent
	// was squashed: the squash is equivalent to none of them individually.
	Absorbed(ctx context.Context, base, branch string) (bool, error)
}

// Restacker is the replay step. It is an interface rather than the service
// itself because sync's own job is ordering, and ordering should be testable
// without standing up a rewrite engine.
type Restacker interface {
	Plan(ctx context.Context, selection graph.Selection, onto restack.Onto, absorb bool, pending restack.Pending) (restack.Plan, error)
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
	// Supersede means the published trunk is not a descendant of this one but
	// contains everything it has by content — somebody rewrote the trunk and
	// pushed it. Taking theirs loses nothing, so the stack is replayed onto it
	// rather than the whole sync refusing.
	Supersede bool
	// DiscardsBase are the trunk commits taking the published trunk would lose,
	// only ever non-empty when the caller asked for that.
	DiscardsBase []string
	// Diverged means the base cannot be reconciled without losing commits.
	// Nothing is attempted in that case: choosing between them is the user's
	// call, not a side effect.
	Diverged bool
	// Collect is the branches of your own whose published version is ahead of
	// the one here, and where it is.
	//
	// Without this a reviewer's commit could not be got onto your machine at
	// all: sync fetched exactly one ref, the base, so a branch you own was
	// never brought down and push then refused because the remote was ahead.
	Collect []Collection
	// Blocked is why an apply would refuse, empty when it would proceed.
	Blocked string
	// Repair is Blocked in the shape a caller can lay out. Where sync refuses
	// it offers a choice — take the published trunk, or reconcile it yourself —
	// and a sentence holding both is where a reader loses which words belong
	// to which.
	Repair repair.Note
}

// Collection is one branch of yours the remote has moved on, and how.
type Collection struct {
	Branch string
	// To is the published tip this branch would be brought to.
	To string
	// Discards are the commits taking the published version would lose. It is
	// only ever non-empty when the caller asked for that with --take, and the
	// preview names every one of them: this is the only way sync loses work.
	Discards []string
	// Superseded means the published version is not a descendant of this one
	// but contains everything it has by content — somebody rebased or amended
	// your branch and published it. Taking theirs keeps their work and loses
	// none of yours, and it is a reset rather than a fast-forward, so it is
	// named rather than treated as the same thing.
	Superseded bool
}

// onto names the base the replay should land on. Until the base branch is
// advanced it is still where it was, so the fetched ref is what the replay has
// to target; when it is already level, the recorded structure already says.
func (p Plan) onto() string {
	if !p.Advance && !p.Supersede {
		return ""
	}
	return localgit.IsolatedRef(p.Remote, p.Base)
}

// Ready reports a service with everything it needs.
//
// One rule, called by both the guard below and the command registration in
// internal/cli. They were two hand-written conjunctions before, and three of
// them had already drifted -- a command could be registered and then refuse on
// use, or be hidden from a build that could have run it.
//
// sync fetches, advances a base and replays, and the replay writes the
// recorded graph, so it needs all three. The registration gate asked for the
// store and the guard asked for the restacker, so a build with one and not the
// other either registered a command that fails on use or hid one that works.
func (s Service) Ready() bool {
	return s.Git != nil && s.Restack != nil && s.Graph.Store != nil
}

// Plan works out the whole sequence without performing any of it. The fetch is
// the one step that reaches the network, and it writes only into g2g's own
// ref namespace, so previewing costs the repository nothing.
func (s Service) Plan(ctx context.Context, selection graph.Selection, remote string, take Take) (Plan, error) {
	if !s.Ready() {
		return Plan{}, fmt.Errorf("sync service is not fully configured")
	}
	if err := s.Git.Remote(ctx, remote); err != nil {
		return Plan{}, err
	}
	// The stack being synced: its trunk, so there is a base to advance, and
	// everything above the target, so the replay covers what depends on it.
	// Cousins that merely share the trunk are somebody else's stack — unless
	// the caller asked for trunk, which is exactly the request to include them.
	discovery, err := s.Graph.Discover(ctx, graph.Selection{Branch: selection.Branch, Scope: syncScope(selection.Scope)})
	if err != nil {
		return Plan{}, err
	}
	// A selection of one is the branch itself with nothing recorded under it,
	// so there is no base to bring up to date.
	if len(discovery.Branches) < 2 {
		return Plan{}, fmt.Errorf("%q has no recorded parent to sync against · run g2g track to record one", discovery.Target)
	}
	plan := Plan{Remote: remote, Base: discovery.Branches[0]}

	// The base and everything selected, in one fetch. Fetching only the base is
	// what left a reviewer's commit unreachable from here.
	wanted := fetchList(plan.Base, discovery.Branches)
	published, err := s.Git.RemoteTips(ctx, remote, wanted)
	if err != nil {
		return Plan{}, err
	}
	stale, err := s.stale(ctx, remote, wanted, published)
	if err != nil {
		return Plan{}, err
	}
	if len(stale) != 0 {
		if err := s.Git.FetchIsolated(ctx, remote, stale); err != nil {
			return Plan{}, err
		}
	}
	// A boundary naming something outside the selection resolves nothing and
	// would look exactly like an ordinary refusal, so it is refused itself.
	if take.Bounded() && !slices.Contains(discovery.Branches, take.Through) && take.Through != plan.Base {
		plan.Repair = repair.Note{
			Reason: fmt.Sprintf("--through %s is not in the stack being synced", take.Through),
			Ways: []repair.Step{
				{Effect: "name a branch this sync selects, or drop --through to take the whole stack"},
			},
		}
		plan.Blocked = plan.Repair.Sentence()
		return plan, nil
	}
	parents := make(map[string]string, len(discovery.Graph.Edges))
	for branch, edge := range discovery.Graph.Edges {
		parents[branch] = edge.Parent
	}
	plan.Advance, plan.Supersede, plan.Diverged, plan.DiscardsBase, err = s.compare(ctx, plan.Base, remote, published, take)
	if err != nil {
		return Plan{}, err
	}
	if plan.Diverged {
		// Same reasoning as a branch: say that both sides moved, not that you
		// have something the remote does not, which is true of any commit.
		plan.Repair = repair.Note{
			Reason: fmt.Sprintf("both sides have moved on %s · it and %s/%s each hold commits the other does not", plan.Base, remote, plan.Base),
			Ways:   divergenceWays(selection, take, parents, nil),
		}
		plan.Blocked = plan.Repair.Sentence()
		return plan, nil
	}

	// The replay is planned against the base as it will be, which is why the
	// fetch and the fast-forward assessment come first.
	// A location, never a parent: the trunk is about to be here, and recording
	// a ref under refs/g2g/ as the parent is what broke every synced stack.
	collect, stuck, err := s.collect(ctx, remote, plan.Base, discovery.Branches, published, take, parents)
	if err != nil {
		return Plan{}, err
	}
	if len(stuck) != 0 {
		plan.Repair = repair.Note{Reason: divergenceReason(stuck), Ways: divergenceWays(selection, take, parents, stuck)}
		plan.Blocked = plan.Repair.Sentence()
		return plan, nil
	}
	plan.Collect = collect
	plan.Restack, err = s.Restack.Plan(ctx, selection, restack.ToLocation(plan.onto()), false, plan.pending())
	if err != nil {
		return Plan{}, err
	}
	// Its structure comes with it. A refusal that arrives from the step this
	// delegates to is no less actionable for having been delegated, and
	// carrying only the sentence handed every machine reader a null where the
	// ways out were.
	plan.Blocked, plan.Repair = plan.Restack.Blocked, plan.Restack.Repair
	// A fork whose replay conflicts is taken one line at a time, and from a
	// leaf sync's own stack scope is exactly that line — whereas the restack
	// command restack offers takes a scope sync does not.
	if len(plan.Restack.Lines) != 0 {
		ways := make([]repair.Step, 0, len(plan.Restack.Lines))
		for _, leaf := range plan.Restack.Lines {
			ways = append(ways, repair.Step{Command: "g2g pull --branch " + leaf, Effect: "bring the line of descent ending at " + leaf + " up to date"})
		}
		plan.Repair = repair.Note{Reason: plan.Restack.Repair.Reason, Ways: ways}
		plan.Blocked = plan.Repair.Sentence()
	}
	diagnostic.Event(ctx, "sync.plan",
		diagnostic.Field{Key: "base", Value: plan.Base},
		diagnostic.Field{Key: "advance", Value: fmt.Sprintf("%t", plan.Advance)},
		diagnostic.Field{Key: "replays", Value: strings.Join(plan.Restack.Replaying(), ",")},
	)
	return plan, nil
}

// Fetched says what g2g's own fetched refs already hold for each branch, from
// local refs. It is optional: without it every branch the remote has is
// fetched, which is what happened before it existed.
type Fetched interface {
	IsolatedTips(ctx context.Context, remote string) (map[string]string, error)
}

// stale is the branches the remote has at a tip g2g has not fetched yet.
//
// A plan fetched every branch the remote has on every run, and a land runs one
// after each merge — right after waiting for the merge by fetching the trunk,
// and again to revalidate — so most of those fetches were of refs already
// here. A ref already at the remote's tip holds every object that tip needs.
func (s Service) stale(ctx context.Context, remote string, wanted []string, published map[string]string) ([]string, error) {
	var fetched map[string]string
	if reader, ok := s.Git.(Fetched); ok {
		var err error
		if fetched, err = reader.IsolatedTips(ctx, remote); err != nil {
			return nil, err
		}
	}
	stale := make([]string, 0, len(wanted))
	for _, branch := range wanted {
		if tip := published[branch]; tip != "" && fetched[branch] != tip {
			stale = append(stale, branch)
		}
	}
	return stale, nil
}

// Nothing reports a plan with no step to take: the base is level and there is
// nothing to replay.
func (p Plan) Nothing() bool {
	return !p.Advance && !p.Supersede && len(p.Collect) == 0 && len(p.Restack.Steps) == 0
}

// Equal compares every fact that changes what the sync does.
func (p Plan) Equal(other Plan) bool {
	return p.Remote == other.Remote &&
		collectionsEqual(p.Collect, other.Collect) &&
		p.Base == other.Base &&
		p.Advance == other.Advance &&
		p.Diverged == other.Diverged &&
		p.Supersede == other.Supersede &&
		slices.Equal(p.DiscardsBase, other.DiscardsBase) &&
		p.Blocked == other.Blocked &&
		p.Restack.Equal(other.Restack)
}

// Revalidate repeats the whole discovery immediately before the mutation and
// refuses if anything moved underneath.
//
// sync had none. It was the one mutating command that wrote its own
// preview-and-apply sequence instead of using the shared flow, and the copy
// left this step out — so it could fetch, advance a base and replay against a
// plan the reader had approved some time earlier.
func (s Service) Revalidate(ctx context.Context, selection graph.Selection, remote string, take Take, preview Plan) (Plan, error) {
	current, err := s.Plan(ctx, selection, remote, take)
	if err != nil {
		return Plan{}, err
	}
	if err := diagnostic.Revalidated(ctx, "sync", "plan", current.Equal(preview)); err != nil {
		return Plan{}, err
	}
	return current, nil
}

// Apply performs the sequence and stops at the first step that cannot finish.
//
// It reports how far it got rather than unwinding: a replay that stops on a
// conflict is resumable, and undoing the fetch and the fast-forward would
// throw away work the user then has to redo.
func (s Service) Apply(ctx context.Context, plan Plan) error {
	if plan.Blocked != "" {
		return fmt.Errorf("cannot sync: %s", plan.Blocked)
	}
	moved := make([]string, 0, len(plan.Collect)+1)
	stop := func(err error) error {
		if len(moved) == 0 {
			return err
		}
		return &Stopped{Moved: moved, Err: err}
	}
	if plan.Advance || plan.Supersede {
		diagnostic.Event(ctx, "sync.advance",
			diagnostic.Field{Key: "base", Value: plan.Base},
			diagnostic.Field{Key: "supersede", Value: fmt.Sprintf("%t", plan.Supersede)},
		)
		move := s.Git.FastForward
		if plan.Supersede {
			// The published trunk is not a descendant of this one, so this is a
			// reset. FastForward would refuse it, correctly.
			move = s.Git.ResetBranch
		}
		if err := move(ctx, plan.Base, localgit.IsolatedRef(plan.Remote, plan.Base)); err != nil {
			return err
		}
		moved = append(moved, plan.Base)
	}
	// Before the replay, because the replay works from the tips these leave
	// behind: a reviewer's commit has to be on the branch before it is moved.
	for _, collection := range plan.Collect {
		diagnostic.Event(ctx, "sync.collect_branch",
			diagnostic.Field{Key: "branch", Value: collection.Branch},
			diagnostic.Field{Key: "superseded", Value: fmt.Sprintf("%t", collection.Superseded)},
		)
		move := s.Git.FastForward
		if collection.Superseded {
			// Not a fast-forward: the published version is not a descendant of
			// this one, so FastForward would refuse it, correctly.
			move = s.Git.ResetBranch
		}
		if err := move(ctx, collection.Branch, collection.To); err != nil {
			return stop(err)
		}
		moved = append(moved, collection.Branch)
	}
	if len(plan.Restack.Steps) != 0 {
		if err := s.Restack.Apply(ctx, plan.Restack); err != nil {
			return stop(err)
		}
	}
	return nil
}

// Stopped is a sync that moved some branches and then failed.
//
// The trunk it advanced and the branches it brought down stay where they are:
// they are what the remote holds, and putting them back would only put them
// behind again. A replay that failed and put its own refs back says "nothing
// was changed" about the replay, which was true of the replay and not of the
// run.
type Stopped struct {
	// Moved are the branches brought to their published versions, trunk
	// first, before the step that failed.
	Moved []string
	Err   error
}

func (s *Stopped) Error() string {
	return fmt.Sprintf("stopped after bringing %s up to date: %v", strings.Join(s.Moved, ", "), s.Err)
}

func (s *Stopped) Unwrap() error { return s.Err }

// syncScope is the boundary this sync acts on.
//
// The default is the stack: the trunk moved, so everything above it is stale.
// trunk widens that to every stack on the same trunk, which is the whole of
// what a person means by "the trunk moved, bring everything up to date". The
// value is validated at the flag, so anything else here is a caller that did
// not go through it, and the default is the safe reading.
func syncScope(scope graph.Scope) graph.Scope {
	if scope == graph.ScopeTrunk {
		return graph.ScopeTrunk
	}
	return graph.ScopeStack
}

// pending is where collect will leave each branch, which is what the replay has
// to be planned against: collect runs first, so by the time the rewrite happens
// a collected branch is no longer where Git said it was when this was planned.
//
// The base is one of them when sync advances it. restack never moves a trunk
// itself, so without being told it would not ask whether another worktree has
// the trunk checked out — the ordinary layout of a checkout with several — and
// sync would move it underneath that worktree.
func (p Plan) pending() restack.Pending {
	if len(p.Collect) == 0 && p.onto() == "" {
		return nil
	}
	moving := make(restack.Pending, len(p.Collect)+1)
	for _, collection := range p.Collect {
		moving[collection.Branch] = collection.To
	}
	if onto := p.onto(); onto != "" {
		moving[p.Base] = onto
	}
	return moving
}

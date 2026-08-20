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
}

// Restacker is the replay step. It is an interface rather than the service
// itself because sync's own job is ordering, and ordering should be testable
// without standing up a rewrite engine.
type Restacker interface {
	Plan(ctx context.Context, selection graph.Selection, onto restack.Onto, absorb bool) (restack.Plan, error)
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
}

// Take is which side wins where sync would otherwise refuse.
//
// It is an enum rather than a boolean flag because the question has more
// answers than the one implemented, and naming the value leaves room for them.
// There is deliberately no "mine": sync only ever moves toward this checkout
// and push only ever moves toward the remote, so which side wins is normally
// answered by which command you run. This exists for the case neither command
// can otherwise reach.
type Take string

const (
	// TakeNothing is the default: a divergence that would cost commits is
	// reported rather than resolved.
	TakeNothing Take = ""
	// TakePublished discards local commits the published version does not have,
	// on the branches where the two have genuinely diverged.
	TakePublished Take = "published"
)

// Takes are the values a caller may name.
var Takes = []Take{TakePublished}

// ParseTake validates a flag value.
func ParseTake(value string) (Take, error) {
	if value == "" {
		return TakeNothing, nil
	}
	for _, take := range Takes {
		if Take(value) == take {
			return take, nil
		}
	}
	names := make([]string, 0, len(Takes))
	for _, take := range Takes {
		names = append(names, string(take))
	}
	return TakeNothing, fmt.Errorf("unsupported --take %q (want %s)", value, strings.Join(names, ", "))
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

// Plan works out the whole sequence without performing any of it. The fetch is
// the one step that reaches the network, and it writes only into g2g's own
// ref namespace, so previewing costs the repository nothing.
func (s Service) Plan(ctx context.Context, selection graph.Selection, remote string, take Take) (Plan, error) {
	if s.Git == nil || s.Restack == nil {
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
	present := make([]string, 0, len(wanted))
	for _, branch := range wanted {
		if published[branch] != "" {
			present = append(present, branch)
		}
	}
	if len(present) != 0 {
		if err := s.Git.FetchIsolated(ctx, remote, present); err != nil {
			return Plan{}, err
		}
	}
	plan.Advance, plan.Supersede, plan.Diverged, plan.DiscardsBase, err = s.compare(ctx, plan.Base, remote, take)
	if err != nil {
		return Plan{}, err
	}
	if plan.Diverged {
		// Same reasoning as a branch: say that both sides moved, not that you
		// have something the remote does not, which is true of any commit.
		plan.Blocked = fmt.Sprintf("both sides have moved on %s · it and %s/%s each hold commits the other does not · take the published trunk with g2g sync --take published, which discards yours, or reconcile it yourself",
			plan.Base, remote, plan.Base)
		return plan, nil
	}

	// The replay is planned against the base as it will be, which is why the
	// fetch and the fast-forward assessment come first.
	// A location, never a parent: the trunk is about to be here, and recording
	// a ref under refs/g2g/ as the parent is what broke every synced stack.
	plan.Collect, plan.Blocked, err = s.collect(ctx, remote, plan.Base, discovery.Branches, take)
	if err != nil {
		return Plan{}, err
	}
	if plan.Blocked != "" {
		return plan, nil
	}
	plan.Restack, err = s.Restack.Plan(ctx, selection, restack.ToLocation(plan.onto()), false)
	if err != nil {
		return Plan{}, err
	}
	plan.Blocked = plan.Restack.Blocked
	diagnostic.Event(ctx, "sync.plan",
		diagnostic.Field{Key: "base", Value: plan.Base},
		diagnostic.Field{Key: "advance", Value: fmt.Sprintf("%t", plan.Advance)},
		diagnostic.Field{Key: "replays", Value: strings.Join(plan.Restack.Replaying(), ",")},
	)
	return plan, nil
}

// compare asks how the base stands against the remote without changing it.
func (s Service) compare(ctx context.Context, base, remote string, take Take) (advance, supersede, diverged bool, discards []string, err error) {
	fetched := localgit.IsolatedRef(remote, base)
	local, err := s.Git.Resolve(ctx, base)
	if err != nil {
		return false, false, false, nil, err
	}
	upstream, err := s.Git.Resolve(ctx, fetched)
	if err != nil {
		// The base is not on the remote at all, which is ordinary for a local
		// trunk that was never pushed.
		return false, false, false, nil, nil
	}
	if local == upstream {
		return false, false, false, nil, nil
	}
	behind, err := s.Git.IsAncestor(ctx, base, fetched)
	if err != nil {
		return false, false, false, nil, err
	}
	if behind {
		return true, false, false, nil, nil
	}
	// Neither side is an ancestor of the other, which is what somebody
	// force-pushing the trunk looks like — usually after rebasing or squashing
	// it. If everything the local trunk has is in the published one by content,
	// nothing is lost by taking theirs, and that is the whole of the recovery:
	// remote trunk, local stack replayed onto it.
	ours, _, err := s.Git.Cherry(ctx, fetched, base, "")
	if err != nil {
		return false, false, true, nil, nil
	}
	if len(ours) == 0 {
		return false, true, false, nil, nil
	}
	if take == TakePublished {
		return false, true, false, ours, nil
	}
	return false, false, true, nil, nil
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
	if err := diagnostic.Revalidated(ctx, "sync.revalidation", "plan", current.Equal(preview)); err != nil {
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
			return err
		}
	}
	if len(plan.Restack.Steps) != 0 {
		if err := s.Restack.Apply(ctx, plan.Restack); err != nil {
			return err
		}
	}
	return nil
}

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

// collect works out which branches of your own the remote has moved on.
//
// Four answers, and only the last is a refusal:
//
//   - not published, or level: nothing to do.
//   - published ahead of here: fast-forward, which is a reviewer pushing a fix
//     onto your branch.
//   - published elsewhere but containing everything here by content: somebody
//     rebased or amended your branch and published it, so theirs supersedes.
//   - genuinely diverged: you have work the published version does not, and
//     choosing between them is not something to do behind your back.
func (s Service) collect(ctx context.Context, remote, base string, branches []string, take Take) ([]Collection, string, error) {
	collect := make([]Collection, 0, len(branches))
	stuck := make([]divergence, 0)
	for _, branch := range branches {
		if branch == base {
			continue
		}
		published, err := s.Git.Resolve(ctx, localgit.IsolatedRef(remote, branch))
		if err != nil {
			// Not on the remote at all, which is ordinary for work in progress.
			continue
		}
		local, err := s.Git.Resolve(ctx, branch)
		if err != nil {
			return nil, "", err
		}
		if local == published {
			continue
		}
		behind, err := s.Git.IsAncestor(ctx, branch, published)
		if err != nil {
			return nil, "", err
		}
		if behind {
			collect = append(collect, Collection{Branch: branch, To: published})
			continue
		}
		ahead, err := s.Git.IsAncestor(ctx, published, branch)
		if err != nil {
			return nil, "", err
		}
		if ahead {
			// You have unpublished work. That is push's business, not sync's.
			continue
		}
		ours, _, err := s.Git.Cherry(ctx, published, branch, "")
		if err != nil {
			return nil, "", err
		}
		if len(ours) == 0 {
			collect = append(collect, Collection{Branch: branch, To: published, Superseded: true})
			continue
		}
		if take == TakePublished {
			// Asked for explicitly, and the commits it costs are carried so the
			// preview can name every one before anything happens.
			collect = append(collect, Collection{Branch: branch, To: published, Superseded: true, Discards: ours})
			continue
		}
		// Both sides moved, so say both. "You have work the remote does not" is
		// true of every ordinary commit, and a reader who has just made one has
		// no way to tell that from this.
		theirs, _, err := s.Git.Cherry(ctx, branch, localgit.IsolatedRef(remote, branch), "")
		if err != nil {
			return nil, "", err
		}
		stuck = append(stuck, divergence{Branch: branch, Ours: len(ours), Theirs: len(theirs)})
	}
	if len(stuck) != 0 {
		return nil, divergenceReason(stuck), nil
	}
	diagnostic.Event(ctx, "sync.collect", diagnostic.Field{Key: "branches", Value: fmt.Sprint(len(collect))})
	return collect, "", nil
}

// fetchList is the base and the selection, each named once. The base is
// normally the first selected branch as well, and asking for it twice put the
// same refspec in the command twice.
//
// What the remote actually has is asked separately, because git fetch fails the
// whole command on one ref it cannot find — and a branch that is gone because
// it merged is the commonest reason for it not to be there.
func fetchList(base string, branches []string) []string {
	wanted := []string{base}
	for _, branch := range branches {
		if branch != base {
			wanted = append(wanted, branch)
		}
	}
	return wanted
}

// collectionsEqual compares collections, including the commits each would
// discard. A plan that would lose different work is a different plan, and
// revalidation has to see that.
func collectionsEqual(left, right []Collection) bool {
	return slices.EqualFunc(left, right, func(a, b Collection) bool {
		return a.Branch == b.Branch && a.To == b.To && a.Superseded == b.Superseded &&
			slices.Equal(a.Discards, b.Discards)
	})
}

// divergence is one branch both sides have moved, and by how much.
type divergence struct {
	Branch       string
	Ours, Theirs int
}

// divergenceReason says which branches both sides moved, and what each side
// holds that the other does not.
//
// Naming only one side was the problem: "you have work the published version
// does not" is true of every ordinary commit, so a reader who had just made one
// could not tell whether that was what the message meant. Both counts make it
// unambiguous, and the counts are by content, so a commit the other side
// already has under a different id is not counted against you.
func divergenceReason(stuck []divergence) string {
	parts := make([]string, 0, len(stuck))
	for _, moved := range stuck {
		parts = append(parts, fmt.Sprintf("%s (%d here that %s not published, %d published that %s not here)",
			moved.Branch,
			moved.Ours, pick(moved.Ours, "is", "are"),
			moved.Theirs, pick(moved.Theirs, "is", "are")))
	}
	return fmt.Sprintf("both sides have moved on %s · take the published version with g2g sync --take published, which discards yours, or reconcile it yourself",
		strings.Join(parts, ", "))
}

// pick chooses the form that agrees with a count.
func pick(count int, one, many string) string {
	if count == 1 {
		return one
	}
	return many
}

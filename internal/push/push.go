// Package push implements the explicit Git-only stack-ref escape hatch.
package push

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/stack"
	"github.com/shhac/g2g/internal/syncpoint"
)

type Git interface {
	stack.Git
	Remote(context.Context, string) error
	RemoteTips(context.Context, string, []string) (map[string]string, error)
	PushAtomic(context.Context, string, []localgit.Lease, localgit.Upstream) error
	// Resolve and Divergence are what turn the observed remote tips into a
	// statement about what the push would do. Both read locally: the tips are
	// already in hand from the one ls-remote, so saying what they mean costs
	// nothing more over the network.
	Resolve(context.Context, string) (string, error)
	Divergence(ctx context.Context, other, target string) (ahead, behind int, err error)
	// Cherry and Absorbed say whether a branch has work the base does not, by
	// content. A branch whose work is entirely in the base has nothing to
	// publish, however absent it is from the remote -- and it takes both to
	// know: Cherry cannot see a squash merge, which is the commonest way the
	// branch that is missing from the remote got that way.
	Cherry(ctx context.Context, upstream, head, limit string) (absent, present []string, err error)
	Absorbed(ctx context.Context, base, branch string) (bool, error)
	IsAncestor(ctx context.Context, ancestor, descendant string) (bool, error)
}

type Service struct {
	Git Git
	// Selector supplies the selection, from whichever source describes the
	// branch. push only publishes refs, so it works with any of them.
	Selector stack.PathSelector
}

type Plan struct {
	stack.Snapshot
	Remote string
	// RemoteTips is what the remote held when the plan was built. It is the
	// lease the push asserts, so a branch that moved in between is rejected
	// rather than overwritten.
	RemoteTips map[string]string
	// Upstream is whether the push records each branch as tracking what it
	// publishes. It is part of the plan rather than of the call that executes
	// it, so a preview and the apply it approves cannot disagree about it.
	Upstream localgit.Upstream
	// Publishing says what the push would do to each branch. The tips alone
	// could not: the preview rendered the same three lines whether a branch was
	// two commits ahead, already published, or behind a commit somebody else
	// pushed — and the last of those is a force-push the lease would reject,
	// previewed without a word about it.
	Publishing map[string]Publication
	// Repair is why an apply would refuse and the ways out, empty when it
	// would proceed. What it names is a git command rather than a g2g one,
	// which is exactly the case where a reader needs to see where it starts
	// and ends before copying it.
	Repair repair.Note
}

// Blocked is why an apply would refuse, as one sentence, empty when it would
// proceed.
func (p Plan) Blocked() string { return p.Repair.Sentence() }

// NothingToPublish reports a plan where the remote already holds every selected
// branch exactly. Pushing would be a no-op, and saying so beats reporting a
// successful push that moved nothing. A branch with no entry was never
// compared, and reads as Uncompared rather than as up to date.
func (p Plan) NothingToPublish() bool {
	for _, branch := range p.Branches {
		if !p.Publishing[branch].UpToDate() {
			return false
		}
	}
	return len(p.Branches) != 0
}

// pushArgs is the exact invocation Execute makes, so a diagnostic never
// advertises a command that differs from the one that runs.
func (p Plan) pushArgs() []string {
	return localgit.PushArguments(p.Remote, p.Leases(), p.Upstream)
}

// Ready reports a service with everything it needs.
//
// One rule, called by both the guard below and the command registration in
// internal/cli. They were two hand-written conjunctions before, and three of
// them had already drifted -- a command could be registered and then refuse on
// use, or be hidden from a build that could have run it.
func (s Service) Ready() bool {
	return s.Git != nil && s.Selector != nil
}

// Leases pairs each selected branch with the tip the plan observed for it,
// leaving out a branch that has landed. Its remote branch is usually gone
// because it merged, and a lease on the absent value would put it back. A
// branch already published exactly stays in: git moves nothing for it, and
// naming it is what lets --set-upstream record what it tracks.
func (p Plan) Leases() []localgit.Lease {
	leases := make([]localgit.Lease, 0, len(p.Branches))
	for _, branch := range p.Branches {
		if p.Publishing[branch].Standing == Landed {
			continue
		}
		leases = append(leases, localgit.Lease{Branch: branch, Expected: p.RemoteTips[branch]})
	}
	return leases
}

func (s Service) Plan(ctx context.Context, selection stack.Selection, remote string, upstream localgit.Upstream) (Plan, error) {
	if !s.Ready() {
		return Plan{}, fmt.Errorf("push service is not fully configured")
	}
	if err := s.Git.Remote(ctx, remote); err != nil {
		return Plan{}, err
	}
	snapshot, err := s.Selector.Select(ctx, selection, "git push")
	if err != nil {
		return Plan{}, err
	}
	// A fork is published whole: one atomic push has no order to keep, and
	// each branch's lease and comparison are its own. The linear projections
	// are submit's and link's, onto GitHub, not this one's.
	if len(snapshot.Branches) == 0 {
		return Plan{}, fmt.Errorf("selected Graphite path has no non-trunk branches to push")
	}
	tips, err := s.Git.RemoteTips(ctx, remote, snapshot.Branches)
	if err != nil {
		return Plan{}, err
	}
	// A selection stands on one base, forked or not.
	publishing, err := Compare(ctx, s.Git, snapshot.Branches, tips, func(branch string) (string, string) {
		return snapshot.SitsOn(branch), snapshot.Base
	})
	if err != nil {
		return Plan{}, err
	}
	s.recordLevel(ctx, remote, snapshot.Branches, publishing, tips)
	plan := Plan{Snapshot: snapshot, Remote: remote, RemoteTips: tips, Upstream: upstream, Publishing: publishing, Repair: blockedBy(remote, snapshot.Branches, publishing, tips)}
	diagnostic.Event(ctx, "push.plan",
		diagnostic.Field{Key: "decision", Value: "ready"},
		diagnostic.Field{Key: "target", Value: snapshot.Target},
		diagnostic.Field{Key: "target_source", Value: snapshot.TargetSource},
		diagnostic.Field{Key: "scope", Value: string(selection.EffectiveScope())},
		diagnostic.Field{Key: "base", Value: snapshot.Base},
		diagnostic.Field{Key: "remote", Value: remote},
		diagnostic.Field{Key: "branches", Value: strings.Join(snapshot.Branches, ",")},
		diagnostic.Field{Key: "command", Value: diagnostic.SafeCommand("git", plan.pushArgs())},
	)
	return plan, nil
}

// blockedBy refuses a push the remote would reject.
//
// A pinned lease protects against movement after preview, not against knowingly
// replacing remote-only work. That needs the user's explicit choice.
func blockedBy(remote string, branches []string, publishing map[string]Publication, tips map[string]string) repair.Note {
	rejected := make([]string, 0, len(branches))
	for _, branch := range branches {
		if publishing[branch].Rejected() {
			rejected = append(rejected, branch)
		}
	}
	if len(rejected) == 0 {
		return repair.Note{}
	}
	leases := make([]localgit.Lease, 0, len(rejected))
	for _, branch := range rejected {
		leases = append(leases, localgit.Lease{Branch: branch, Expected: tips[branch]})
	}
	replace := repair.Command(append([]string{"git"}, localgit.PushArguments(remote, leases, localgit.LeaveUpstream)...))
	reconcile := "fetch and reconcile first"
	for _, branch := range rejected {
		if publishing[branch].Standing == Diverged {
			reconcile += "; a conflict-resolved replay can also change the published patch, so check the differences before replacing it"
			break
		}
	}
	// Naming the command that does work matters more than the refusal. No g2g
	// command republishes over a remote that has moved, and deliberately
	// dropping a published commit is a real thing to want, so a preview that
	// only says no leaves the reader with nowhere to go.
	return repair.Note{
		Reason: fmt.Sprintf("the published version differs on %s", strings.Join(rejected, ", ")),
		Ways: []repair.Step{
			{Effect: reconcile},
			{
				Command: replace,
				Effect:  "replace what is published, dropping what the remote has",
			},
		},
	}
}

func (s Service) Execute(ctx context.Context, plan Plan) error {
	if s.Git == nil {
		return fmt.Errorf("push service is not fully configured")
	}
	// push cannot reach the pull request source at all, so this can only ever
	// be a no-op here. It is asked anyway: the reason push is safe is a flag
	// gate two packages away, and a mutation should not depend on remembering
	// that.
	if err := plan.Snapshot.RequireActionable("g2g push"); err != nil {
		return err
	}
	diagnostic.Event(ctx, "push.apply",
		diagnostic.Field{Key: "decision", Value: "run"},
		diagnostic.Field{Key: "remote", Value: plan.Remote},
		diagnostic.Field{Key: "branches", Value: strings.Join(plan.Branches, ",")},
		diagnostic.Field{Key: "command", Value: diagnostic.SafeCommand("git", plan.pushArgs())},
	)
	leases := plan.Leases()
	// A push naming no branch is not a no-op: git falls back to push.default
	// and publishes whatever that chooses.
	if len(leases) == 0 {
		return nil
	}
	if err := s.Git.PushAtomic(ctx, plan.Remote, leases, plan.Upstream); err != nil {
		return err
	}
	s.recordPushed(ctx, plan, leases)
	return nil
}

// recordLevel notes the agreement a branch the remote already holds exactly is
// in, which is a fact about the two rather than a decision, so a preview may
// record it. It is what gives a branch nobody has pushed through g2g yet a
// sync point to measure the next change from.
func (s Service) recordLevel(ctx context.Context, remote string, branches []string, publishing map[string]Publication, tips map[string]string) {
	recorder, ok := s.Git.(syncpoint.ReadRecorder)
	if !ok {
		return
	}
	for _, branch := range branches {
		if publishing[branch].Standing != Current {
			continue
		}
		point := localgit.SyncPoint{Tip: tips[branch], Local: tips[branch], Command: "push"}
		if err := syncpoint.Agree(ctx, recorder, remote, branch, point); err != nil {
			diagnostic.Event(ctx, "push.sync_point", diagnostic.Field{Key: "branch", Value: branch}, diagnostic.Field{Key: "decision", Value: "not recorded"})
		}
	}
}

// recordPushed notes that this clone and the remote now agree on every branch
// the push named, which is what lets a later pull or push tell a commit
// somebody dropped from one nobody had. The push has happened by now, so a
// recording that fails is a diagnostic rather than a failure: the next
// agreement records it, and until then the cautious reading applies.
func (s Service) recordPushed(ctx context.Context, plan Plan, leases []localgit.Lease) {
	recorder, ok := s.Git.(syncpoint.ReadRecorder)
	if !ok {
		return
	}
	for _, lease := range leases {
		tip, err := s.Git.Resolve(ctx, lease.Branch)
		if err == nil {
			err = syncpoint.Agree(ctx, recorder, plan.Remote, lease.Branch, localgit.SyncPoint{Tip: tip, Local: tip, Command: "push"})
		}
		if err != nil {
			diagnostic.Event(ctx, "push.sync_point",
				diagnostic.Field{Key: "branch", Value: lease.Branch},
				diagnostic.Field{Key: "decision", Value: "not recorded"},
			)
		}
	}
}

// Equal compares every fact that changes what the push does, including the
// remote tips the leases assert: a branch that moved on the remote between
// preview and apply must stop the push, not be overwritten by it.
func (p Plan) Equal(other Plan) bool {
	return p.Snapshot.Equal(other.Snapshot) &&
		p.Repair.Equal(other.Repair) &&
		maps.Equal(p.Publishing, other.Publishing) &&
		p.Remote == other.Remote &&
		p.Upstream == other.Upstream &&
		maps.Equal(p.RemoteTips, other.RemoteTips)
}

var _ Git = localgit.Client{}

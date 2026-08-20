// Package push implements the explicit Git-only stack-ref escape hatch.
package push

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/stack"
)

type Git interface {
	stack.Git
	Remote(context.Context, string) error
	RemoteTips(context.Context, string, []string) (map[string]string, error)
	PushAtomic(context.Context, string, []localgit.Lease) error
	// Resolve and Divergence are what turn the observed remote tips into a
	// statement about what the push would do. Both read locally: the tips are
	// already in hand from the one ls-remote, so saying what they mean costs
	// nothing more over the network.
	Resolve(context.Context, string) (string, error)
	Divergence(ctx context.Context, other, target string) (ahead, behind int, err error)
	// Cherry says whether a branch has work the base does not, by content. A
	// branch whose work is entirely in the base has nothing to publish, however
	// absent it is from the remote.
	Cherry(ctx context.Context, upstream, head, limit string) (absent, present []string, err error)
}

type Service struct {
	Git Git
	// Selector supplies the ordered path, from whichever source describes the
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
	// Publishing says what the push would do to each branch. The tips alone
	// could not: the preview rendered the same three lines whether a branch was
	// two commits ahead, already published, or behind a commit somebody else
	// pushed — and the last of those is a force-push the lease would reject,
	// previewed without a word about it.
	Publishing map[string]Publication
	// Blocked is why an apply would refuse, empty when it would proceed.
	Blocked string
}

// Publication is what pushing one branch would do.
type Publication struct {
	// Ours is how many commits the local branch has that the remote does not.
	// Theirs is how many the remote has that this repository does not.
	Ours, Theirs int
	// New means the remote has no such branch yet, so there is nothing to
	// compare and nothing to overwrite.
	New bool
	// Landed means the branch has no work the base does not already have. A
	// branch that merged and was deleted looks exactly like a new one from the
	// remote's side, and offering to put it back is the wrong reading: it is
	// gone because it is finished.
	Landed bool
	// Unknown means the remote is on a commit this repository does not have,
	// so the two cannot be compared without fetching. It is treated exactly
	// like being behind, because that is what it most likely is.
	Unknown bool
}

// NothingToPublish reports a plan where the remote already holds every selected
// branch exactly. Pushing would be a no-op, and saying so beats reporting a
// successful push that moved nothing.
func (p Plan) NothingToPublish() bool {
	for _, branch := range p.Branches {
		// A branch with no entry was never compared, and the zero Publication
		// reads as "up to date" — the most reassuring thing it could possibly
		// mean. Requiring the entry is what stops a plan that skipped the
		// comparison from claiming the remote already has everything.
		publication, compared := p.Publishing[branch]
		if !compared || !publication.UpToDate() {
			return false
		}
	}
	return len(p.Branches) != 0
}

// Rejected reports a branch the lease would refuse: the remote holds something
// this push would overwrite.
func (p Publication) Rejected() bool { return p.Theirs > 0 || p.Unknown }

// UpToDate reports a branch the remote needs nothing from: either it already
// has it exactly, or the branch has nothing left to give.
func (p Publication) UpToDate() bool {
	if p.Landed {
		return true
	}
	return !p.New && !p.Unknown && p.Ours == 0 && p.Theirs == 0
}

// pushArgs is the exact invocation Execute makes, so a diagnostic never
// advertises a command that differs from the one that runs.
func (p Plan) pushArgs() []string {
	args := []string{"push", "--atomic"}
	for _, lease := range p.Leases() {
		args = append(args, lease.Argument())
	}
	args = append(args, p.Remote)
	return append(args, p.Branches...)
}

// Leases pairs each selected branch with the tip the plan observed for it.
func (p Plan) Leases() []localgit.Lease {
	leases := make([]localgit.Lease, 0, len(p.Branches))
	for _, branch := range p.Branches {
		leases = append(leases, localgit.Lease{Branch: branch, Expected: p.RemoteTips[branch]})
	}
	return leases
}

func (s Service) Plan(ctx context.Context, selection stack.Selection, remote string) (Plan, error) {
	if s.Git == nil || s.Selector == nil {
		return Plan{}, fmt.Errorf("push service is not fully configured")
	}
	if err := s.Git.Remote(ctx, remote); err != nil {
		return Plan{}, err
	}
	snapshot, err := s.Selector.Select(ctx, selection, "git push")
	if err != nil {
		return Plan{}, err
	}
	// One atomic push of an ordered path is the whole contract here, so a
	// selection that forks is refused rather than pushed in some order.
	if err := snapshot.RequireLinear("push"); err != nil {
		return Plan{}, err
	}
	if len(snapshot.Branches) == 0 {
		return Plan{}, fmt.Errorf("selected Graphite path has no non-trunk branches to push")
	}
	tips, err := s.Git.RemoteTips(ctx, remote, snapshot.Branches)
	if err != nil {
		return Plan{}, err
	}
	publishing, err := s.publications(ctx, snapshot.Base, snapshot.Branches, tips)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{Snapshot: snapshot, Remote: remote, RemoteTips: tips, Publishing: publishing, Blocked: blockedBy(remote, snapshot.Branches, publishing)}
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

// publications compares each selected branch with the tip the remote holds.
//
// A remote tip this repository does not have is not an error: it is what being
// behind looks like before a fetch, and refusing to plan would be a worse
// answer than saying so.
func (s Service) publications(ctx context.Context, base string, branches []string, tips map[string]string) (map[string]Publication, error) {
	publishing := make(map[string]Publication, len(branches))
	for _, branch := range branches {
		tip, published := tips[branch]
		if !published || tip == "" {
			// Absent from the remote has two meanings, and they want opposite
			// answers: work nobody has seen, or work that merged and took the
			// branch with it.
			own, _, err := s.Git.Cherry(ctx, base, branch, "")
			if err != nil {
				return nil, err
			}
			publishing[branch] = Publication{New: len(own) != 0, Landed: len(own) == 0}
			continue
		}
		local, err := s.Git.Resolve(ctx, branch)
		if err != nil {
			return nil, err
		}
		if local == tip {
			publishing[branch] = Publication{}
			continue
		}
		if _, err := s.Git.Resolve(ctx, tip); err != nil {
			publishing[branch] = Publication{Unknown: true}
			continue
		}
		theirs, ours, err := s.Git.Divergence(ctx, tip, branch)
		if err != nil {
			return nil, err
		}
		publishing[branch] = Publication{Ours: ours, Theirs: theirs}
	}
	return publishing, nil
}

// blockedBy refuses a push the remote would reject.
//
// The lease already rejects it, so this changes no outcome — it moves the
// refusal in front of the network call and says which branch, instead of
// inviting an --apply that fails at git.
func blockedBy(remote string, branches []string, publishing map[string]Publication) string {
	rejected := make([]string, 0, len(branches))
	for _, branch := range branches {
		if publishing[branch].Rejected() {
			rejected = append(rejected, branch)
		}
	}
	if len(rejected) == 0 {
		return ""
	}
	// Naming the command that does work matters more than the refusal. No g2g
	// command republishes over a remote that has moved, and deliberately
	// dropping a published commit is a real thing to want, so a preview that
	// only says no leaves the reader with nowhere to go.
	return fmt.Sprintf("the remote has moved on %s · fetch and reconcile first, or if you meant to replace what is published: git push --force-with-lease %s %s",
		strings.Join(rejected, ", "), remote, strings.Join(rejected, " "))
}

func (s Service) Revalidate(ctx context.Context, selection stack.Selection, remote string, preview Plan) (Plan, error) {
	plan, err := s.Plan(ctx, selection, remote)
	if err != nil {
		return Plan{}, err
	}
	return plan, diagnostic.Revalidated(ctx, "push", "push plan", plan.Equal(preview))
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
	return s.Git.PushAtomic(ctx, plan.Remote, plan.Leases())
}

// Equal compares every fact that can change which refs are pushed where.
// Equal compares every fact that changes what the push does, including the
// remote tips the leases assert: a branch that moved on the remote between
// preview and apply must stop the push, not be overwritten by it.
func (p Plan) Equal(other Plan) bool {
	return p.Snapshot.Equal(other.Snapshot) &&
		p.Blocked == other.Blocked &&
		maps.Equal(p.Publishing, other.Publishing) &&
		p.Remote == other.Remote &&
		maps.Equal(p.RemoteTips, other.RemoteTips)
}

var _ Git = localgit.Client{}

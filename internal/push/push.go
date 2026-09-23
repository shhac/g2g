// Package push implements the explicit Git-only stack-ref escape hatch.
package push

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/landed"
	"github.com/shhac/g2g/internal/parallel"
	"github.com/shhac/g2g/internal/repair"
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
	// Repair is Blocked in the shape a caller can lay out. What it names is a
	// git command rather than a g2g one, which is exactly the case where a
	// reader needs to see where it starts and ends before copying it.
	Repair repair.Note
}

// Publication is where one branch stands against the remote's tip, and so what
// pushing it would do.
type Publication struct {
	Standing Standing
	// Ours is how many commits the local branch has that the remote's tip does
	// not, which is what the push sends. Theirs is how many of the remote's
	// commits have no equivalent in the branch, by content: the work a push
	// would drop. Counting those by commit id called every replayed commit
	// somebody else's, so a restacked stack could never be published.
	Ours, Theirs int
}

// Standing is one of the ways a branch and the remote's tip can differ. They
// are exclusive, which is why this is one value rather than a flag each: every
// reader of the flags had to work the exclusivity out again, in its own order,
// and got the same answer only because of how Compare happened to set them.
type Standing int

const (
	// Uncompared is the zero value, so a branch nobody compared reads as
	// exactly that. Reading as "up to date" was the one wrong answer the zero
	// value used to give, and every reader carried a flag to avoid it.
	Uncompared Standing = iota
	// Current means the remote holds this branch exactly.
	Current
	// New means the remote has no such branch yet, so there is nothing to
	// compare and nothing to overwrite.
	New
	// Landed means the branch has no work the base does not already have. A
	// branch that merged and was deleted looks exactly like a new one from the
	// remote's side, and offering to put it back is the wrong reading: it is
	// gone because it is finished.
	Landed
	// Unknown means the remote is on a commit this repository does not have,
	// so the two cannot be compared without fetching. It is treated exactly
	// like being behind, because that is what it most likely is.
	Unknown
	// Ahead means the branch has Ours commits on top of what the remote holds.
	Ahead
	// Behind means the remote has Theirs commits the branch does not.
	Behind
	// Diverged means both: Ours here, and Theirs a push would drop.
	Diverged
	// Rewritten means the published version is not an ancestor of the branch
	// and yet holds nothing the branch lacks, by content: the branch was
	// replayed since it was pushed. Publishing replaces the old version and
	// loses nothing, which is the ordinary state after a restack.
	Rewritten
)

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

// Rejected reports a branch the lease would refuse: the remote holds something
// this push would overwrite.
func (p Publication) Rejected() bool {
	return p.Standing == Unknown || p.Standing == Behind || p.Standing == Diverged
}

// UpToDate reports a branch the remote needs nothing from: either it already
// has it exactly, or the branch has nothing left to give.
func (p Publication) UpToDate() bool {
	return p.Standing == Current || p.Standing == Landed
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

// Ready reports a service with everything it needs.
//
// One rule, called by both the guard below and the command registration in
// internal/cli. They were two hand-written conjunctions before, and three of
// them had already drifted -- a command could be registered and then refuse on
// use, or be hidden from a build that could have run it.
func (s Service) Ready() bool {
	return s.Git != nil && s.Selector != nil
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
	// A push is of one linear path, so the branch below each is the one before
	// it and every one stands on the same base.
	publishing, err := Compare(ctx, s.Git, snapshot.Branches, tips, func(branch string) (string, string) {
		return parentOf(snapshot.Base, snapshot.Branches, branch), snapshot.Base
	})
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{Snapshot: snapshot, Remote: remote, RemoteTips: tips, Publishing: publishing, Repair: blockedBy(remote, snapshot.Branches, publishing)}
	plan.Blocked = plan.Repair.Sentence()
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

// Comparer is the part of Git a comparison reads, all of it local: the tips
// are already in hand, so saying what they mean costs nothing over the network.
type Comparer interface {
	landed.Lineage
	Resolve(context.Context, string) (string, error)
	Divergence(ctx context.Context, other, target string) (ahead, behind int, err error)
}

// Compare says what publishing each branch over the given remote tips would
// do. below names the branch each one sits on and the trunk its stack stands
// on: the first is what a squashed parent's commits would have landed in, the
// second what a branch missing from the remote may have landed in.
//
// A remote tip this repository does not have is not an error: it is what being
// behind looks like before a fetch, and refusing to plan would be a worse
// answer than saying so.
//
// Each branch is its own few process spawns and none depends on another, so
// they are asked at once, as status's other per-branch reads are: doctor asks
// this of every recorded branch.
func Compare(ctx context.Context, git Comparer, branches []string, tips map[string]string, below func(string) (parent, trunk string)) (map[string]Publication, error) {
	// Sized before the reads start, so each owns one element and needs no lock.
	results := make([]Publication, len(branches))
	err := parallel.Each(ctx, branches, func(ctx context.Context, index int, branch string) error {
		parent, trunk := below(branch)
		publication, err := compareOne(ctx, git, branch, tips[branch], parent, trunk)
		results[index] = publication
		return err
	})
	if err != nil {
		return nil, err
	}
	publishing := make(map[string]Publication, len(branches))
	for index, branch := range branches {
		publishing[branch] = results[index]
	}
	return publishing, nil
}

// compareOne is where one branch stands against the tip the remote holds for
// it, which is empty when the remote has no such branch.
func compareOne(ctx context.Context, git Comparer, branch, tip, parent, trunk string) (Publication, error) {
	if tip == "" {
		// Absent from the remote has two meanings, and they want opposite
		// answers: work nobody has seen, or work that merged and took the
		// branch with it. Asking per commit alone got the second one wrong on
		// the commonest way a branch lands -- a squash leaves no commit with an
		// equivalent, so a branch that merged and was deleted read as new and
		// was offered for republication.
		upstream, err := landed.Into(ctx, git, trunk, branch, "")
		if err != nil || !upstream {
			return Publication{Standing: New}, err
		}
		return Publication{Standing: Landed}, nil
	}
	local, err := git.Resolve(ctx, branch)
	if err != nil {
		return Publication{}, err
	}
	if local == tip {
		return Publication{Standing: Current}, nil
	}
	if _, err := git.Resolve(ctx, tip); err != nil {
		return Publication{Standing: Unknown}, nil
	}
	behind, ours, err := git.Divergence(ctx, tip, branch)
	if err != nil {
		return Publication{}, err
	}
	if behind == 0 {
		return Publication{Standing: Ahead, Ours: ours}, nil
	}
	if ours == 0 {
		// Only behind: the branch has nothing the remote lacks, so every one of
		// the remote's commits is missing here by definition. Comparing their
		// content said the same thing at the cost of all of them — a trunk not
		// updated for a while is thousands.
		return Publication{Standing: Behind, Theirs: behind}, nil
	}
	// The remote tip is not an ancestor. Whether that loses anything is a
	// question of content, and it is the same one status asks of a pull
	// request's head, asked the same way.
	theirs, err := landed.Missing(ctx, git, branch, tip, parent)
	if err != nil {
		return Publication{}, err
	}
	return Publication{Standing: standingApart(ours, theirs), Ours: ours, Theirs: theirs}, nil
}

// standingApart names a branch and a remote tip that are not in order, from
// what each holds that the other does not.
func standingApart(ours, theirs int) Standing {
	switch {
	case theirs == 0:
		return Rewritten
	case ours == 0:
		return Behind
	default:
		return Diverged
	}
}

// parentOf is the branch below this one on the path, which is what a squashed
// parent's commits would have landed in.
func parentOf(base string, branches []string, branch string) string {
	below := base
	for _, candidate := range branches {
		if candidate == branch {
			return below
		}
		below = candidate
	}
	return base
}

// blockedBy refuses a push the remote would reject.
//
// The lease already rejects it, so this changes no outcome — it moves the
// refusal in front of the network call and says which branch, instead of
// inviting an --apply that fails at git.
func blockedBy(remote string, branches []string, publishing map[string]Publication) repair.Note {
	rejected := make([]string, 0, len(branches))
	for _, branch := range branches {
		if publishing[branch].Rejected() {
			rejected = append(rejected, branch)
		}
	}
	if len(rejected) == 0 {
		return repair.Note{}
	}
	// Naming the command that does work matters more than the refusal. No g2g
	// command republishes over a remote that has moved, and deliberately
	// dropping a published commit is a real thing to want, so a preview that
	// only says no leaves the reader with nowhere to go.
	return repair.Note{
		Reason: fmt.Sprintf("the remote has moved on %s", strings.Join(rejected, ", ")),
		Ways: []repair.Step{
			{Effect: "fetch and reconcile first"},
			{
				Command: fmt.Sprintf("git push --force-with-lease %s %s", remote, strings.Join(rejected, " ")),
				Effect:  "replace what is published, dropping what the remote has",
			},
		},
	}
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

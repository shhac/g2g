package create

import (
	"context"
	"fmt"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/landed"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/words"
)

// Where a new branch starts, and what switching to it from a detached HEAD
// would leave behind.

// resolveStart chooses the pinned start and proves that switching to it keeps
// the original detached HEAD reachable, independently of the rollback target.
func (s Service) resolveStart(ctx context.Context, plan Plan, requested string) (Plan, error) {
	at, err := s.Git.Resolve(ctx, plan.Parent)
	if err != nil {
		return Plan{}, err
	}
	plan.At, plan.ParentAt, plan.AtSource = at, at, requested
	if requested != "" {
		if err := subprocess.CheckArgument("git", "starting revision", requested); err != nil {
			return plan.refuse(repair.Note{Reason: err.Error()}), nil
		}
		plan.At, err = s.Git.Resolve(ctx, requested)
		if err != nil {
			return Plan{}, err
		}
		note, err := s.startAt(ctx, plan)
		if err != nil {
			return Plan{}, err
		}
		if note.Reason != "" {
			return plan.refuse(note), nil
		}
	}
	lost, err := s.leavesBehind(ctx, plan)
	if err != nil {
		return Plan{}, err
	}
	if lost {
		return detachedRefusal(plan), nil
	}
	return plan, nil
}

// leavesBehind reports a detached HEAD whose commits switching to the new
// branch would leave to the reflog alone.
//
// The start itself is the one commit known to lose nothing. Past that, a HEAD
// some branch, tag or remote's copy still reaches loses nothing either, nor
// does one whose work the start already has by content -- by commit, or as
// the squash a stack merged into the trunk leaves, which is landed's question
// and asked through it. Refusing those sent people detached at a landed
// stack's tip to detach somewhere else first, when the --at they had already
// passed said where to start. A reader that cannot ask refuses, as create
// always did.
func (s Service) leavesBehind(ctx context.Context, plan Plan) (bool, error) {
	head := plan.ReturnTip
	if head == "" || head == plan.At {
		return false, nil
	}
	git, ok := s.Git.(reachability)
	if !ok {
		return true, nil
	}
	unreferenced, err := git.Unreferenced(ctx, head)
	if err != nil || !unreferenced {
		return unreferenced, err
	}
	there, err := landed.Into(ctx, git, plan.At, head, "")
	return !there, err
}

// reachability is the optional part of a reader that can say whether leaving
// a detached HEAD loses anything: whether any ref reaches it, and whether
// the start already has its work.
type reachability interface {
	landed.Probe
	Unreferenced(ctx context.Context, revision string) (bool, error)
}

// detachedRefusal refuses switching away from a detached HEAD whose commits
// would be left behind, with the two ways out: keep them, or let them go.
func detachedRefusal(plan Plan) Plan {
	return plan.refuse(repair.Note{
		Reason: fmt.Sprintf("HEAD is detached at commits no branch, tag or remote's copy reaches, and %s does not already have, so switching to %s would leave them behind", startName(plan), plan.Name),
		Ways: []repair.Step{
			{Command: "git branch <name> HEAD", Effect: "keep them on a branch of their own, then rerun"},
			{Command: "git switch --detach " + repair.Quote(plan.Parent), Effect: "detach at " + plan.Parent + "'s tip if they are not wanted, then rerun " + repair.Command([]string{"g2g", "create", plan.Name, "--parent", plan.Parent})},
		},
	})
}

// startName names where the new branch would start, for a sentence.
func startName(plan Plan) string {
	if plan.AtSource != "" {
		return plan.AtSource
	}
	return plan.Parent
}

// evidencedTrunk reports a parent that is a trunk somebody named or the
// repository's default branch: the only parent a start past its tip may be.
func evidencedTrunk(plan Plan) bool {
	graph := plan.Discovery.Graph
	return !graph.Tracked(plan.Parent) && (graph.IsTrunk(plan.Parent) || plan.Discovery.DefaultTrunk == plan.Parent)
}

// StartAtHead is the create that starts at a detached HEAD instead of the
// parent's tip, recorded under the same parent.
func (p Plan) StartAtHead() string {
	return repair.Command([]string{"g2g", "create", p.Name, "--parent", p.Parent, "--at", "HEAD"})
}

// detachedWithoutParent refuses a create with nothing to build on. A worktree
// made at the trunk's remote copy is the usual way to be here, and starting
// there is a create away, so it is offered: never chosen, because a trunk is
// evidenced, never guessed, and naming the parent is the reader's to do.
func (s Service) detachedWithoutParent(ctx context.Context, plan Plan, head string) (Plan, error) {
	note := repair.Note{
		Reason: "HEAD is detached, so there is no branch to build on",
		Ways:   []repair.Step{{Effect: "pass --parent with the branch to build on"}},
	}
	git, ok := s.Git.(interface {
		DefaultBranch(ctx context.Context, remote string) (string, error)
	})
	if !ok {
		return plan.refuse(note), nil
	}
	trunk, err := git.DefaultBranch(ctx, "")
	if err != nil || trunk == "" {
		return plan.refuse(note), err
	}
	tip, err := s.Git.Resolve(ctx, trunk)
	if err != nil {
		// The default branch has no local copy here to record anything under.
		return plan.refuse(note), nil
	}
	if tip == head {
		note.Ways = append([]repair.Step{{Command: repair.Command([]string{"g2g", "create", plan.Name, "--parent", trunk}), Effect: "start at " + trunk + ", where HEAD is, and record it there"}}, note.Ways...)
		return plan.refuse(note), nil
	}
	ahead, err := s.headAhead(ctx, trunk, tip, head)
	if err != nil {
		return Plan{}, err
	}
	if ahead != 0 {
		onTrunk := plan
		onTrunk.Parent = trunk
		note.Ways = append([]repair.Step{{Command: onTrunk.StartAtHead(), Effect: fmt.Sprintf("start at HEAD, %s ahead of %s where its remote copy is, and record it under %s", words.Count(ahead, "commit", "commits"), trunk, trunk)}}, note.Ways...)
	}
	return plan.refuse(note), nil
}

// headAhead is how many commits a detached HEAD is ahead of a trunk's tip
// here while still on that trunk's known upstream -- where a worktree made at
// the trunk's remote copy stands -- or zero when it is anywhere else.
func (s Service) headAhead(ctx context.Context, trunk, tip, head string) (int, error) {
	git, ok := s.Git.(interface {
		graph.Upstream
		IsAncestor(context.Context, string, string) (bool, error)
		Divergence(ctx context.Context, other, target string) (int, int, error)
	})
	if !ok || head == "" || head == tip {
		return 0, nil
	}
	known, err := git.KnownParent(ctx, trunk)
	if err != nil || known == "" {
		return 0, err
	}
	for _, pair := range [][2]string{{tip, head}, {head, known}} {
		ancestor, err := git.IsAncestor(ctx, pair[0], pair[1])
		if err != nil || !ancestor {
			return 0, err
		}
	}
	_, ahead, err := git.Divergence(ctx, tip, head)
	return ahead, err
}

// startAt accepts a newer start only when local upstream evidence proves that
// all inherited work belongs to the trunk. Feature starts remain at their tip.
func (s Service) startAt(ctx context.Context, plan Plan) (repair.Note, error) {
	if plan.At == plan.ParentAt {
		return repair.Note{}, nil
	}
	if !evidencedTrunk(plan) {
		return repair.Note{Reason: "--at away from the parent tip requires an evidenced trunk parent",
			Ways: []repair.Step{{Effect: "omit --at to start at the parent tip"}}}, nil
	}
	refuse := func(reason string) (repair.Note, error) {
		return repair.Note{Reason: reason, Ways: []repair.Step{
			{Command: repair.Command([]string{"g2g", "pull", "--branch", plan.Parent, "--trunk-only"}), Effect: "preview updating the trunk and its local upstream evidence, then retry creation"},
			{Effect: "omit --at to start at the parent tip, or choose a revision contained in the known upstream"},
		}}, nil
	}
	git, ok := s.Git.(interface {
		graph.Upstream
		IsAncestor(context.Context, string, string) (bool, error)
	})
	if !ok {
		return refuse("cannot verify --at against the trunk's known local upstream")
	}
	known, err := git.KnownParent(ctx, plan.Parent)
	if err != nil {
		return repair.Note{}, err
	}
	if known == "" {
		return refuse("--at requires a known local upstream tip for " + plan.Parent)
	}
	for _, pair := range [][2]string{{plan.ParentAt, plan.At}, {plan.At, known}} {
		ancestor, err := git.IsAncestor(ctx, pair[0], pair[1])
		if err != nil {
			return repair.Note{}, err
		}
		if !ancestor {
			return refuse("--at must descend from the parent and be contained in its known local upstream")
		}
	}
	return repair.Note{}, nil
}

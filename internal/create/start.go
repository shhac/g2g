package create

import (
	"context"
	"fmt"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/landed"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/subprocess"
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

// startAt accepts a newer start only when local upstream evidence proves that
// all inherited work belongs to the trunk. Feature starts remain at their tip.
func (s Service) startAt(ctx context.Context, plan Plan) (repair.Note, error) {
	if plan.At == plan.ParentAt {
		return repair.Note{}, nil
	}
	if plan.Discovery.Graph.Tracked(plan.Parent) ||
		(!plan.Discovery.Graph.IsTrunk(plan.Parent) && plan.Discovery.DefaultTrunk != plan.Parent) {
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

package reshape

import (
	"context"
	"fmt"
	"slices"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/subprocess"
)

// RenamePlan is everything a rename will do, decided before it does any of it.
type RenamePlan struct {
	Discovery graph.Discovery
	From      string
	To        string
	// Current is the branch checked out here. git branch -m moves HEAD with
	// the branch, so it changes nothing an apply does and is what a preview
	// says about the checkout.
	Current string
	// Trunk marks a rename of a branch the graph records as a trunk, whose
	// entry in the trunk list moves with it.
	Trunk bool
	// Children are the branches recorded on From, which the write records on
	// To.
	Children []string
	// ForkPoint is From's recorded fork point, whose pin moves to To's name.
	ForkPoint string
	// Remote names the remote-tracking refs still carrying From's name. The
	// published branch and any pull request for it stay under that name.
	Remote []string
	// Elsewhere is the worktree that has From checked out, if another does.
	// Git moves that worktree's HEAD with the rename, so it is reported and
	// not refused.
	Elsewhere string
	Updated   graph.Graph
	Blocked   string
	Repair    repair.Note
}

// Equal compares everything that changes what an apply does or what its
// preview said.
func (p RenamePlan) Equal(other RenamePlan) bool {
	return p.From == other.From &&
		p.To == other.To &&
		p.Current == other.Current &&
		p.Trunk == other.Trunk &&
		p.ForkPoint == other.ForkPoint &&
		p.Elsewhere == other.Elsewhere &&
		p.Blocked == other.Blocked &&
		slices.Equal(p.Children, other.Children) &&
		slices.Equal(p.Remote, other.Remote) &&
		p.Updated.Equal(other.Updated) &&
		p.Discovery.Equal(other.Discovery)
}

func (p RenamePlan) refuse(note repair.Note) RenamePlan {
	p.Repair = note
	p.Blocked = note.Sentence()
	return p
}

// PlanRename decides what renaming a branch to name would do. An empty from
// means the branch checked out.
func (s Service) PlanRename(ctx context.Context, from, name string) (RenamePlan, error) {
	if !s.Ready() {
		return RenamePlan{}, fmt.Errorf("rename is not fully configured")
	}
	discovery, err := s.Graph.Discover(ctx, graph.Selection{Branch: from, Scope: graph.ScopeStack})
	if err != nil {
		return RenamePlan{}, err
	}
	adopted := discovery.Graph
	plan := RenamePlan{Discovery: discovery, From: discovery.Target, To: name, Current: s.currentBranch(ctx), Updated: adopted}
	// An option-like name is refused before git is asked anything about it,
	// because check-ref-format would read it as one of its own flags.
	if err := subprocess.CheckArgument("git", "branch name", name); err != nil {
		return plan.refuse(repair.Note{Reason: err.Error(), Ways: []repair.Step{{Effect: "choose a name that does not begin with a dash"}}}), nil
	}
	if err := s.Git.CheckBranchName(ctx, name); err != nil {
		return plan.refuse(repair.Note{Reason: err.Error(), Ways: []repair.Step{{Effect: "choose another name"}}}), nil
	}
	if plan.From == plan.To {
		return plan.refuse(repair.Note{Reason: fmt.Sprintf("%s already has that name", plan.From)}), nil
	}
	if !adopted.Records(plan.From) {
		return plan.refuse(unrecorded(plan.From, repair.Step{Effect: "or rename it with git branch -m, which is all renaming it here would do"})), nil
	}
	local, err := s.Git.LocalBranches(ctx)
	if err != nil {
		return RenamePlan{}, err
	}
	if !slices.Contains(local, plan.From) {
		return plan.refuse(notLocal(plan.From)), nil
	}
	if slices.Contains(local, plan.To) {
		return plan.refuse(repair.Note{Reason: fmt.Sprintf("a branch named %s already exists", plan.To), Ways: []repair.Step{{Effect: "choose another name"}}}), nil
	}
	if adopted.Records(plan.To) {
		// A record for a branch that is not here is left over from one that
		// was deleted, and it may have children. Renaming onto it would hand
		// them to a branch that has nothing to do with them.
		return plan.refuse(repair.Note{
			Reason: fmt.Sprintf("the graph already records %s, from a branch that no longer exists here", plan.To),
			Ways:   []repair.Step{{Effect: "choose another name, or forget that record first"}},
		}), nil
	}
	if plan.Updated, err = adopted.Rename(plan.From, plan.To); err != nil {
		return RenamePlan{}, err
	}
	plan.Trunk = !adopted.Tracked(plan.From)
	plan.Children = adopted.Children(plan.From)
	plan.ForkPoint = adopted.Edges[plan.From].ForkPoint
	if plan.Remote, err = s.Git.RemoteTracking(ctx, plan.From); err != nil {
		return RenamePlan{}, err
	}
	elsewhere, err := s.Git.CheckedOutElsewhere(ctx)
	if err != nil {
		return RenamePlan{}, err
	}
	plan.Elsewhere = elsewhere[plan.From]
	return plan, nil
}

// RevalidateRename plans again and refuses if anything moved since the
// preview.
func (s Service) RevalidateRename(ctx context.Context, from, name string, preview RenamePlan) (RenamePlan, error) {
	plan, err := s.PlanRename(ctx, from, name)
	if err != nil {
		return RenamePlan{}, err
	}
	return plan, diagnostic.Revalidated(ctx, "rename", "the branch and the stack around it", plan.Equal(preview))
}

// ApplyRename renames the branch, then the record, then the fork-point pin.
//
// The branch goes first because git refuses a rename that would clobber
// anything, and a refusal there has changed nothing. A record or pin that
// cannot follow is put back by renaming the branch back, so the two never
// disagree about what the branch is called. Releasing the old pin is tidying,
// and failing at it leaves a completed rename with a stale ref.
func (s Service) ApplyRename(ctx context.Context, plan RenamePlan) error {
	if plan.Blocked != "" {
		return fmt.Errorf("cannot rename %s: %s", plan.From, plan.Blocked)
	}
	diagnostic.Event(ctx, "reshape.rename.apply", diagnostic.Field{Key: "from", Value: plan.From}, diagnostic.Field{Key: "to", Value: plan.To})
	if err := s.Git.RenameBranch(ctx, plan.From, plan.To); err != nil {
		return err
	}
	done := undo{func(ctx context.Context) error { return s.Git.RenameBranch(ctx, plan.To, plan.From) }}
	fail := func(cause error) error {
		if err := done.run(ctx); err != nil {
			return &RolledBack{Operation: "rename", Branch: plan.From, Cause: cause, Left: err}
		}
		return &RolledBack{Operation: "rename", Branch: plan.From, Cause: cause}
	}
	if err := s.Graph.Store.Save(ctx, plan.Updated); err != nil {
		return fail(err)
	}
	done = append(done, func(ctx context.Context) error { return s.Graph.Store.Save(ctx, plan.Discovery.Graph) })
	if s.Graph.Refs == nil || plan.ForkPoint == "" {
		return nil
	}
	if err := s.Graph.Refs.PinForkPoint(ctx, plan.To, plan.ForkPoint); err != nil {
		return fail(err)
	}
	if err := s.Graph.Refs.UnpinForkPoint(ctx, plan.From); err != nil {
		return &Partial{
			Done: fmt.Sprintf("%s is now %s, and the graph records it under that name", plan.From, plan.To),
			Left: "the fork-point ref under the old name could not be released",
			Err:  err,
		}
	}
	return nil
}

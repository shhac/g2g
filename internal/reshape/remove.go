package reshape

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/landed"
	"github.com/shhac/g2g/internal/repair"
)

// Operation is which of the two removals a plan is: both take a branch out of
// the stack and put its children on what it sat on, and fold first moves its
// parent up to it so its commits stay.
type Operation string

const (
	Delete Operation = "delete"
	Fold   Operation = "fold"
)

// Plan is everything a delete or fold will do, decided before it does any of
// it.
type Plan struct {
	Operation Operation
	// Discovery is the stack the branch is in, which is what a preview draws.
	Discovery graph.Discovery
	Branch    string
	Parent    string
	ForkPoint string
	// Tip and ParentTip are where the two branches are now. Fold moves the
	// parent from ParentTip to Tip, leased on ParentTip.
	Tip       string
	ParentTip string
	// Current is the branch checked out here, empty on a detached HEAD. It
	// decides whether an apply has to move the checkout.
	Current string
	// Children are the branches recorded on Branch, which the write records
	// on Parent instead, each keeping its fork point.
	Children []string
	// Siblings are Parent's other children. A fold moves Parent, so each of
	// them will need a restack afterwards.
	Siblings []string
	// Unique are Branch's commits that exist nowhere else: not in Parent by
	// content, and on no remote-tracking ref. A delete drops them from every
	// child's next restack, and once the branch is gone nothing names them.
	Unique []git.Commit
	// Remote names the remote-tracking refs carrying Branch's name. Nothing
	// here touches them or the remote.
	Remote  []string
	Updated graph.Graph
	// Blocked is why an apply would refuse, and Repair is the same refusal as
	// structure. Blocked is always Repair's sentence.
	Blocked string
	Repair  repair.Note
}

// Equal compares everything that changes what an apply does or what its
// preview said.
func (p Plan) Equal(other Plan) bool {
	return p.Operation == other.Operation &&
		p.Branch == other.Branch &&
		p.Parent == other.Parent &&
		p.ForkPoint == other.ForkPoint &&
		p.Tip == other.Tip &&
		p.ParentTip == other.ParentTip &&
		p.Current == other.Current &&
		p.Blocked == other.Blocked &&
		slices.Equal(p.Children, other.Children) &&
		slices.Equal(p.Siblings, other.Siblings) &&
		slices.Equal(p.Unique, other.Unique) &&
		slices.Equal(p.Remote, other.Remote) &&
		p.Updated.Equal(other.Updated) &&
		p.Discovery.Equal(other.Discovery)
}

// Moves reports whether an apply moves the parent's ref, which only a fold
// of a branch with commits of its own does.
func (p Plan) Moves() bool { return p.Operation == Fold && p.ParentTip != p.Tip }

func (p Plan) refuse(note repair.Note) Plan {
	p.Repair = note
	p.Blocked = note.Sentence()
	return p
}

// Plan decides what deleting or folding a branch would do. An empty branch
// means the one checked out.
func (s Service) Plan(ctx context.Context, operation Operation, requested string) (Plan, error) {
	if !s.Ready() {
		return Plan{}, fmt.Errorf("%s is not fully configured", operation)
	}
	discovery, err := s.Graph.Discover(ctx, graph.Selection{Branch: requested, Scope: graph.ScopeStack})
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{Operation: operation, Discovery: discovery, Branch: discovery.Target, Current: s.currentBranch(ctx), Updated: discovery.Graph}
	adopted := discovery.Graph
	if !adopted.Tracked(plan.Branch) {
		return plan.refuse(untrackedRemoval(operation, adopted, plan.Branch)), nil
	}
	edge := adopted.Edges[plan.Branch]
	plan.Parent, plan.ForkPoint = edge.Parent, edge.ForkPoint

	local, err := s.Git.LocalBranches(ctx)
	if err != nil {
		return Plan{}, err
	}
	if !slices.Contains(local, plan.Branch) {
		return plan.refuse(notLocal(plan.Branch, repair.Step{Command: "g2g untrack --branch " + plan.Branch, Effect: "forget the record of a branch that is already gone"})), nil
	}
	if !slices.Contains(local, plan.Parent) {
		return plan.refuse(repair.Note{
			Reason: fmt.Sprintf("%s is recorded on %s, which is not a local branch, so there is nowhere here to put what sits on it", plan.Branch, plan.Parent),
			Ways:   []repair.Step{{Command: "g2g track --branch " + plan.Branch + " --parent <branch>", Effect: "record it on a branch that is here first"}},
		}), nil
	}
	if plan.Tip, err = s.Git.Resolve(ctx, plan.Branch); err != nil {
		return Plan{}, err
	}
	if plan.ParentTip, err = s.Git.Resolve(ctx, plan.Parent); err != nil {
		return Plan{}, err
	}
	if plan.Updated, plan.Children, err = adopted.Remove(plan.Branch); err != nil {
		return Plan{}, err
	}
	if plan.Remote, err = s.Git.RemoteTracking(ctx, plan.Branch); err != nil {
		return Plan{}, err
	}
	if operation == Fold && !adopted.Tracked(plan.Parent) {
		return plan.refuse(intoTrunk(plan)), nil
	}
	if blocked, refused, err := s.refuseHeld(ctx, plan); err != nil || refused {
		return blocked, err
	}
	if operation == Fold {
		return s.planFold(ctx, plan)
	}
	plan.Unique, err = s.unique(ctx, plan)
	if err != nil {
		return Plan{}, err
	}
	return plan, nil
}

// untrackedRemoval explains why a branch with no recorded parent cannot be
// removed. A trunk is the case worth naming: its stacks would have nothing to
// be recorded on.
func untrackedRemoval(operation Operation, adopted graph.Graph, branch string) repair.Note {
	if adopted.IsDeclared(branch) {
		return repair.Note{
			Reason: fmt.Sprintf("%s is a declared trunk: nothing records what it sits on, so the branches on it would have nowhere to go", branch),
			Ways: []repair.Step{
				{Command: "g2g untrack --branch " + branch, Effect: "stop it being a trunk, stranding what sits on it"},
				{Effect: "then delete it with git branch -D"},
			},
		}
	}
	if adopted.Records(branch) {
		return repair.Note{
			Reason: fmt.Sprintf("%s is a trunk: nothing records what it sits on, so the branches on it would have nowhere to go", branch),
			Ways:   []repair.Step{{Effect: "record the branches on it somewhere else first, with g2g track --parent"}},
		}
	}
	if operation == Delete {
		return unrecorded(branch, repair.Step{Effect: "or delete it with git branch -D, which is all deleting it here would do"})
	}
	return unrecorded(branch)
}

// refuseHeld turns away a branch another worktree has checked out, among those
// an apply would remove or move.
//
// Git will not delete a branch checked out elsewhere, so a delete would fail
// half-way through; and moving a parent held elsewhere leaves that worktree
// describing a commit its branch no longer points at.
func (s Service) refuseHeld(ctx context.Context, plan Plan) (Plan, bool, error) {
	affected := []string{plan.Branch}
	if plan.Moves() {
		affected = append(affected, plan.Parent)
	}
	held, err := s.heldElsewhere(ctx, affected...)
	if err != nil || len(held) == 0 {
		return plan, false, err
	}
	consequence := "git will not delete a branch another worktree is on"
	if plan.Moves() {
		consequence = "it would be left on a branch that has moved or gone"
	}
	return plan.refuse(heldNote(held, consequence)), true, nil
}

// intoTrunk refuses folding a branch into the trunk it sits on.
//
// A branch joins its trunk by being merged, which is what a pull request is
// for. Moving a trunk here would put work on it that no review saw, one push
// away from publishing it.
func intoTrunk(plan Plan) repair.Note {
	return repair.Note{
		Reason: fmt.Sprintf("%s sits on the trunk %s, and folding it would move the trunk", plan.Branch, plan.Parent),
		Ways:   []repair.Step{{Command: "g2g land --branch " + plan.Branch, Effect: "put it into " + plan.Parent + " through its pull request"}},
	}
}

// planFold adds what only a fold needs: a parent it can move by fast-forward
// alone, and the siblings that moving it leaves behind.
func (s Service) planFold(ctx context.Context, plan Plan) (Plan, error) {
	adopted := plan.Discovery.Graph
	ancestor, err := s.Git.IsAncestor(ctx, plan.Parent, plan.Branch)
	if err != nil {
		return Plan{}, err
	}
	if !ancestor {
		return plan.refuse(repair.Note{
			Reason: fmt.Sprintf("%s has moved on since %s was stacked on it, so it cannot be fast-forwarded to %s", plan.Parent, plan.Branch, plan.Branch),
			Ways:   []repair.Step{{Command: "g2g restack --branch " + plan.Branch, Effect: "put " + plan.Branch + " back on top of " + plan.Parent + ", then fold"}},
		}), nil
	}
	for _, sibling := range adopted.Children(plan.Parent) {
		if sibling != plan.Branch {
			plan.Siblings = append(plan.Siblings, sibling)
		}
	}
	return plan, nil
}

// unique lists the branch's commits that nothing else holds: none of their
// content is in the parent, and no remote-tracking ref reaches them.
//
// Whether the branch has landed is asked first and whole, because a squash
// merge puts every commit's content in the parent under one new commit that
// matches none of them, and listing those as about to be lost would be false.
func (s Service) unique(ctx context.Context, plan Plan) ([]git.Commit, error) {
	since := plan.ForkPoint
	if since == "" {
		since = plan.Parent
	}
	done, err := landed.Into(ctx, s.Git, plan.Parent, plan.Branch, since)
	if err != nil || done {
		return nil, err
	}
	absent, _, err := s.Git.Cherry(ctx, plan.Parent, plan.Branch, since)
	if err != nil {
		return nil, err
	}
	unpublished, err := s.Git.Unpublished(ctx, plan.Branch, since)
	if err != nil {
		return nil, err
	}
	unique := make([]git.Commit, 0, len(absent))
	for _, commit := range unpublished {
		if slices.Contains(absent, commit.ID) {
			unique = append(unique, commit)
		}
	}
	return unique, nil
}

// Revalidate plans again and refuses if anything moved since the preview.
func (s Service) Revalidate(ctx context.Context, operation Operation, branch string, preview Plan) (Plan, error) {
	plan, err := s.Plan(ctx, operation, branch)
	if err != nil {
		return Plan{}, err
	}
	return plan, diagnostic.Revalidated(ctx, string(operation), "the branch and the stack around it", plan.Equal(preview))
}

// Apply removes the branch, in an order chosen so that everything up to the
// last step can be put back.
//
// The ref moves and the switch come first, because each is refused by Git
// outright when it would lose something, and a refusal there has changed
// nothing. The record is written before the branch is deleted, because a
// record is the one thing here that can be restored exactly; the branch goes
// last. Releasing its fork-point pin is tidying, and failing at it leaves a
// completed removal with a stale ref rather than a reason to undo one.
func (s Service) Apply(ctx context.Context, plan Plan) error {
	if plan.Blocked != "" {
		return fmt.Errorf("cannot %s %s: %s", plan.Operation, plan.Branch, plan.Blocked)
	}
	diagnostic.Event(ctx, "reshape."+string(plan.Operation)+".apply",
		diagnostic.Field{Key: "branch", Value: plan.Branch},
		diagnostic.Field{Key: "parent", Value: plan.Parent},
		diagnostic.Field{Key: "children", Value: strings.Join(plan.Children, ",")},
	)
	var done undo
	fail := func(cause error) error {
		if len(done) == 0 {
			return cause
		}
		if err := done.run(ctx); err != nil {
			return &RolledBack{Operation: string(plan.Operation), Branch: plan.Branch, Cause: cause, Left: err}
		}
		return &RolledBack{Operation: string(plan.Operation), Branch: plan.Branch, Cause: cause}
	}

	if plan.Moves() {
		if err := s.Git.MoveBranch(ctx, plan.Parent, plan.ParentTip, plan.Tip); err != nil {
			return err
		}
		done = append(done, func(ctx context.Context) error { return s.Git.MoveBranch(ctx, plan.Parent, plan.Tip, plan.ParentTip) })
		if plan.Current == plan.Parent {
			// The ref moved under the checkout, so the tree follows it. read-tree
			// refuses rather than overwrite a local change, and the move is
			// then put back.
			if err := s.Git.SwitchTree(ctx, plan.ParentTip, plan.Tip); err != nil {
				return fail(err)
			}
			done = append(done, func(ctx context.Context) error { return s.Git.SwitchTree(ctx, plan.Tip, plan.ParentTip) })
		}
	}
	if plan.Current == plan.Branch {
		if err := s.Git.SwitchExisting(ctx, plan.Parent); err != nil {
			return fail(err)
		}
		done = append(done, func(ctx context.Context) error { return s.Git.SwitchExisting(ctx, plan.Branch) })
	}
	if err := s.Graph.Store.Save(ctx, plan.Updated); err != nil {
		return fail(err)
	}
	done = append(done, func(ctx context.Context) error { return s.Graph.Store.Save(ctx, plan.Discovery.Graph) })
	if err := s.Git.DeleteBranch(ctx, plan.Branch); err != nil {
		return fail(err)
	}
	if s.Graph.Refs == nil {
		return nil
	}
	if err := s.Graph.Refs.UnpinForkPoint(ctx, plan.Branch); err != nil {
		return &Partial{
			Done: fmt.Sprintf("%s is gone and what sat on it is recorded on %s", plan.Branch, plan.Parent),
			Left: "its fork-point ref could not be released",
			Err:  err,
		}
	}
	return nil
}

// Package create makes a branch on top of another and records it, in one step.
//
// It owns no structure rule of its own. The edge is recorded through the graph
// service's own track plan, so a created branch is recorded exactly as
// `track --parent` would record it; what this adds is the refusal to record a
// child under a branch the graph does not know, which track has no reason to
// make and a new branch always does.
package create

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/subprocess"
)

// Git is what making a branch needs from the repository.
type Git interface {
	CurrentBranch(ctx context.Context) (string, error)
	LocalBranches(ctx context.Context) ([]string, error)
	Resolve(ctx context.Context, revision string) (string, error)
	CheckBranchName(ctx context.Context, name string) error
	StagedPaths(ctx context.Context) ([]string, error)
	CreateBranch(ctx context.Context, name, start string) error
	SwitchExisting(ctx context.Context, branch string) error
	DeleteBranch(ctx context.Context, branch string) error
	Commit(ctx context.Context, message string) error
}

// Graph is the part of the graph service that reads and records one edge.
type Graph interface {
	Discover(ctx context.Context, selection graph.Selection) (graph.Discovery, error)
	PlanTrack(ctx context.Context, selection graph.Selection, parent string) (graph.TrackPlan, error)
	ApplyTrack(ctx context.Context, plan graph.TrackPlan) error
}

type Service struct {
	Git   Git
	Graph Graph
}

// Ready reports a service with everything it needs, and is the rule the
// command's registration uses.
func (s Service) Ready() bool { return s.Git != nil && s.Graph != nil }

// Request is what the user asked for.
type Request struct {
	Name string
	// Parent is empty to mean the branch the checkout is on.
	Parent string
	// Commit asks for the staged changes to be committed onto the new branch
	// with Message.
	Commit  bool
	Message string
}

// Plan is everything an apply will do, decided before it does any of it.
type Plan struct {
	Name   string
	Parent string
	// ParentSource says where the parent came from, because "the branch you
	// are on" and "the branch you named" are different claims to check.
	ParentSource string
	// At is the parent's tip, which is where the branch starts and the fork
	// point its edge records.
	At string
	// Return is the branch checked out now, which is where a failed recording
	// goes back to.
	Return  string
	Commit  bool
	Message string
	Staged  []string
	// NewTrunk names a parent that recording this edge makes a root of the
	// graph, which only the repository's default branch may become here.
	NewTrunk string
	// Discovery is the graph read from the parent's side: the path down to
	// it, and where the store lives.
	Discovery graph.Discovery
	// Blocked is why an apply would refuse, and Repair is the same refusal as
	// structure. Blocked is always Repair's sentence.
	Blocked string
	Repair  repair.Note
}

// Equal compares everything that changes what an apply does.
func (p Plan) Equal(other Plan) bool {
	return p.Name == other.Name &&
		p.Parent == other.Parent &&
		p.ParentSource == other.ParentSource &&
		p.At == other.At &&
		p.Return == other.Return &&
		p.Commit == other.Commit &&
		p.Message == other.Message &&
		p.NewTrunk == other.NewTrunk &&
		p.Blocked == other.Blocked &&
		slices.Equal(p.Staged, other.Staged) &&
		p.Discovery.Equal(other.Discovery)
}

func (p Plan) refuse(note repair.Note) Plan {
	p.Repair = note
	p.Blocked = note.Sentence()
	return p
}

// Plan decides what creating the branch would do, and refuses anything that
// would leave a record every later command has to trust on a guess.
func (s Service) Plan(ctx context.Context, request Request) (Plan, error) {
	if !s.Ready() {
		return Plan{}, fmt.Errorf("create is not fully configured")
	}
	plan := Plan{Name: request.Name, Parent: request.Parent, ParentSource: "--parent", Commit: request.Commit, Message: request.Message}
	// Where a failed recording goes back to. A detached HEAD has no branch to
	// go back to, and putting someone somewhere other than where they were is
	// not a rollback.
	current, err := s.Git.CurrentBranch(ctx)
	if err != nil {
		return plan.refuse(repair.Note{
			Reason: "HEAD is detached, so there is no branch to return to if recording fails",
			Ways:   []repair.Step{{Effect: "switch to a branch first"}},
		}), nil
	}
	plan.Return = current
	if plan.Parent == "" {
		plan.Parent, plan.ParentSource = current, "current Git branch"
	}
	if blocked, refused := s.refuseName(ctx, plan); refused {
		return blocked, nil
	}
	local, err := s.Git.LocalBranches(ctx)
	if err != nil {
		return Plan{}, err
	}
	if slices.Contains(local, plan.Name) {
		return plan.refuse(repair.Note{
			Reason: fmt.Sprintf("a branch named %s already exists", plan.Name),
			Ways: []repair.Step{
				{Command: "g2g track --branch " + plan.Name + " --parent " + plan.Parent, Effect: "record the existing branch under " + plan.Parent + " instead"},
				{Effect: "choose another name"},
			},
		}), nil
	}
	if err := subprocess.CheckArgument("git", "parent branch", plan.Parent); err != nil {
		return plan.refuse(repair.Note{Reason: err.Error()}), nil
	}
	if !slices.Contains(local, plan.Parent) {
		return plan.refuse(repair.Note{Reason: fmt.Sprintf("parent %s is not a local branch", plan.Parent)}), nil
	}
	discovery, err := s.Graph.Discover(ctx, graph.Selection{Branch: plan.Parent, Scope: graph.ScopePath})
	if err != nil {
		return Plan{}, err
	}
	plan.Discovery = discovery
	at, err := s.Git.Resolve(ctx, plan.Parent)
	if err != nil {
		return Plan{}, err
	}
	plan.At = at
	if blocked, refused := refuseRecord(plan, discovery); refused {
		return blocked, nil
	}
	if !recorded(discovery.Graph, plan.Parent) {
		plan.NewTrunk = plan.Parent
	}
	if !plan.Commit {
		return plan, nil
	}
	staged, err := s.Git.StagedPaths(ctx)
	if err != nil {
		return Plan{}, err
	}
	plan.Staged = staged
	return refuseCommit(plan), nil
}

// refuseName turns away a name before it reaches anything that could misread
// it: one that is option-like would be read by git as a flag, so it is refused
// before git is asked whether it is valid.
func (s Service) refuseName(ctx context.Context, plan Plan) (Plan, bool) {
	if err := subprocess.CheckArgument("git", "branch name", plan.Name); err != nil {
		return plan.refuse(repair.Note{Reason: err.Error(), Ways: []repair.Step{{Effect: "choose a name that does not begin with a dash"}}}), true
	}
	if err := s.Git.CheckBranchName(ctx, plan.Name); err != nil {
		return plan.refuse(repair.Note{Reason: err.Error(), Ways: []repair.Step{{Effect: "choose another name"}}}), true
	}
	return plan, false
}

// refuseRecord is the rule create adds over track.
//
// Recording a child under a branch the graph does not know makes that branch a
// root — so a feature branch that was never recorded would silently become a
// trunk, and every later command would treat the stack as starting there. The
// repository's default branch is the one exception, because it is a trunk.
func refuseRecord(plan Plan, discovery graph.Discovery) (Plan, bool) {
	adopted := discovery.Graph
	if adopted.Tracked(plan.Name) || adopted.IsTrunk(plan.Name) || len(adopted.Children(plan.Name)) != 0 {
		// A record for a branch that is not here is left over from one that was
		// deleted, and it may have children. Reusing the name would hand them
		// to a branch that has nothing to do with them.
		return plan.refuse(repair.Note{
			Reason: fmt.Sprintf("the graph already records %s, from a branch that no longer exists here", plan.Name),
			Ways:   []repair.Step{{Effect: "choose another name, or forget that record first"}},
		}), true
	}
	if recorded(adopted, plan.Parent) || plan.Parent == discovery.DefaultTrunk {
		return plan, false
	}
	return plan.refuse(repair.Note{
		Reason: fmt.Sprintf("%s is not in the g2g graph, so recording %s under it would make %s a trunk", plan.Parent, plan.Name, plan.Parent),
		Ways: []repair.Step{
			{Command: "g2g track --stack --branch " + plan.Parent, Effect: "record the stack " + plan.Parent + " is on first"},
			{Effect: "pass --parent with a branch the graph records"},
			// Nothing here can tell a trunk from a feature branch without
			// evidence, and the repository did not say. Recording a first branch
			// by hand is the user saying so, and after it create works here.
			{Effect: "if " + plan.Parent + " is a trunk, start its first branch with git switch -c and record it with g2g track --parent " + plan.Parent},
		},
	}), true
}

// recorded reports a branch the graph places: tracked under something, or a
// root something is tracked under.
func recorded(adopted graph.Graph, branch string) bool {
	return adopted.Tracked(branch) || adopted.IsTrunk(branch) || len(adopted.Children(branch)) != 0
}

func refuseCommit(plan Plan) Plan {
	switch {
	case strings.TrimSpace(plan.Message) == "":
		return plan.refuse(repair.Note{Reason: "the commit message is empty", Ways: []repair.Step{{Effect: "pass a message with -m"}}})
	case len(plan.Staged) == 0:
		return plan.refuse(repair.Note{
			Reason: "nothing is staged, so there is nothing to commit",
			Ways: []repair.Step{
				{Effect: "stage what the first commit should hold, then rerun"},
				{Command: "g2g create " + plan.Name, Effect: "create the branch without committing"},
			},
		})
	}
	return plan
}

// Revalidate plans again and refuses if anything moved since the preview.
func (s Service) Revalidate(ctx context.Context, request Request, preview Plan) (Plan, error) {
	plan, err := s.Plan(ctx, request)
	if err != nil {
		return Plan{}, err
	}
	return plan, diagnostic.Revalidated(ctx, "create", "the branch to create", plan.Equal(preview))
}

// Partial is an apply that created and recorded the branch and then could not
// commit. Both of those stay: the branch is where the user asked for it and
// recorded where they said, and the staged changes are still staged on it.
type Partial struct {
	Branch string
	Parent string
	Err    error
}

func (p *Partial) Error() string {
	return fmt.Sprintf("created and recorded %s, but the commit failed: %v", p.Branch, p.Err)
}
func (p *Partial) Unwrap() error { return p.Err }

// Apply creates the branch, records it, and commits, in that order.
//
// Recording comes before the commit so a recording that fails can be undone
// completely: the branch has nothing of its own yet, so switching back and
// deleting it loses nothing. Committing first would put the staged work in a
// commit that the rollback then deletes.
func (s Service) Apply(ctx context.Context, plan Plan) error {
	if plan.Blocked != "" {
		return fmt.Errorf("cannot create %q: %s", plan.Name, plan.Blocked)
	}
	diagnostic.Event(ctx, "create.apply", diagnostic.Field{Key: "branch", Value: plan.Name}, diagnostic.Field{Key: "parent", Value: plan.Parent})
	if err := s.Git.CreateBranch(ctx, plan.Name, plan.Parent); err != nil {
		return err
	}
	if err := s.record(ctx, plan); err != nil {
		return s.rollback(ctx, plan, err)
	}
	if !plan.Commit {
		return nil
	}
	if err := s.Git.Commit(ctx, plan.Message); err != nil {
		return &Partial{Branch: plan.Name, Parent: plan.Parent, Err: err}
	}
	return nil
}

// record writes the edge exactly as track would, and refuses one that is not
// the edge the preview promised.
func (s Service) record(ctx context.Context, plan Plan) error {
	tracked, err := s.Graph.PlanTrack(ctx, graph.Selection{Branch: plan.Name}, plan.Parent)
	if err != nil {
		return err
	}
	if tracked.Blocked != "" {
		return errors.New(tracked.Blocked)
	}
	// The parent can only have moved in the moment between the revalidation
	// and the switch, and a fork point other than the commit the branch
	// started at would make its first restack replay the wrong range.
	if forkPoint := tracked.Updated.Edges[plan.Name].ForkPoint; forkPoint != plan.At {
		return fmt.Errorf("%s moved while %s was being created", plan.Parent, plan.Name)
	}
	return s.Graph.ApplyTrack(ctx, tracked)
}

// rollback puts the checkout back where it was and removes the branch, which
// has no commits of its own yet. It says what it could not undo rather than
// hiding it behind the error that caused it.
func (s Service) rollback(ctx context.Context, plan Plan, cause error) error {
	if err := s.Git.SwitchExisting(ctx, plan.Return); err != nil {
		return &RolledBack{Branch: plan.Name, Cause: cause, Left: fmt.Errorf("could not switch back to %s: %w", plan.Return, err)}
	}
	if err := s.Git.DeleteBranch(ctx, plan.Name); err != nil {
		return &RolledBack{Branch: plan.Name, Cause: cause, Left: fmt.Errorf("could not delete %s: %w", plan.Name, err)}
	}
	return &RolledBack{Branch: plan.Name, Cause: cause}
}

// RolledBack is a recording that failed after the branch was created. Left is
// what the rollback could not undo, nil when it undid everything.
type RolledBack struct {
	Branch string
	Cause  error
	Left   error
}

func (r *RolledBack) Error() string {
	if r.Left != nil {
		return fmt.Sprintf("recording %s failed (%v), and the rollback did not finish: %v", r.Branch, r.Cause, r.Left)
	}
	return fmt.Sprintf("recording %s failed, so it was removed and the checkout is back where it was: %v", r.Branch, r.Cause)
}
func (r *RolledBack) Unwrap() error { return r.Cause }

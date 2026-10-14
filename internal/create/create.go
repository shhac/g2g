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
	"fmt"
	"slices"
	"strings"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/shape"
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
	SwitchDetached(ctx context.Context, revision string) error
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
	// goes back to. Empty means HEAD is detached, which a plan allows only at
	// At, so a failed recording detaches there again.
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
	// Repair is why an apply would refuse and the ways out, empty when it
	// would proceed.
	Repair repair.Note
}

// Blocked is why an apply would refuse, as one sentence, empty when it would
// proceed.
func (p Plan) Blocked() string { return p.Repair.Sentence() }

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
		p.Repair.Equal(other.Repair) &&
		slices.Equal(p.Staged, other.Staged) &&
		p.Discovery.Equal(other.Discovery)
}

func (p Plan) refuse(note repair.Note) Plan {
	p.Repair = note
	return p
}

// Plan decides what creating the branch would do, and refuses anything that
// would leave a record every later command has to trust on a guess.
func (s Service) Plan(ctx context.Context, request Request) (Plan, error) {
	if !s.Ready() {
		return Plan{}, fmt.Errorf("create is not fully configured")
	}
	plan := Plan{Name: request.Name, Parent: request.Parent, ParentSource: "--parent", Commit: request.Commit, Message: request.Message}
	// Where a failed recording goes back to: the branch checked out now, or
	// the commit a detached HEAD is at, which detaching again restores.
	current, err := s.Git.CurrentBranch(ctx)
	detached := err != nil
	if detached && plan.Parent == "" {
		return plan.refuse(repair.Note{
			Reason: "HEAD is detached, so there is no branch to build on",
			Ways:   []repair.Step{{Effect: "pass --parent with the branch to build on"}},
		}), nil
	}
	head := ""
	if detached {
		if head, err = s.Git.Resolve(ctx, "HEAD"); err != nil {
			return Plan{}, err
		}
	}
	plan.Return = current
	if plan.Parent == "" {
		plan.Parent, plan.ParentSource = current, shape.TargetCurrent
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
				{Command: "g2g track --branch " + repair.Quote(plan.Name) + " --parent " + repair.Quote(plan.Parent), Effect: "record the existing branch under " + plan.Parent + " instead"},
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
	if blocked, refused := refuseDetachedAway(plan, head); refused {
		return blocked, nil
	}
	if blocked, refused := refuseRecord(plan, discovery); refused {
		return blocked, nil
	}
	if !discovery.Graph.Records(plan.Parent) {
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

// refuseDetachedAway refuses switching away from a detached HEAD anywhere but
// the parent's tip. Switching leaves behind whatever only HEAD reaches, and
// the parent's tip is the one commit known to lose nothing. head is empty for
// a HEAD on a branch.
func refuseDetachedAway(plan Plan, head string) (Plan, bool) {
	if head == "" || head == plan.At {
		return plan, false
	}
	return plan.refuse(repair.Note{
		Reason: fmt.Sprintf("HEAD is detached somewhere other than %s's tip, so switching to %s could leave commits behind", plan.Parent, plan.Name),
		Ways: []repair.Step{
			{Command: "git switch --detach " + repair.Quote(plan.Parent), Effect: "detach at " + plan.Parent + "'s tip, then rerun"},
			{Effect: "switch to a branch first"},
		},
	}), true
}

// refuseRecord is the rule create adds over track.
//
// Recording a child under a branch the graph does not know makes that branch a
// root — so a feature branch that was never recorded would silently become a
// trunk, and every later command would treat the stack as starting there. The
// repository's default branch is the one exception, because it is a trunk.
func refuseRecord(plan Plan, discovery graph.Discovery) (Plan, bool) {
	adopted := discovery.Graph
	if adopted.Records(plan.Name) {
		// A record for a branch that is not here is left over from one that was
		// deleted, and it may have children. Reusing the name would hand them
		// to a branch that has nothing to do with them.
		return plan.refuse(repair.Note{
			Reason: fmt.Sprintf("the graph already records %s, from a branch that no longer exists here", plan.Name),
			Ways:   []repair.Step{{Effect: "choose another name, or forget that record first"}},
		}), true
	}
	if adopted.Records(plan.Parent) || plan.Parent == discovery.DefaultTrunk {
		return plan, false
	}
	return plan.refuse(untrustedParent(plan, discovery.DefaultTrunk == "")), true
}

// untrustedParent is the refusal for a parent nothing establishes. Where no
// default branch is known it says so, because that is the evidence that would
// have let create accept the parent, and without it the refusal reads as
// arbitrary to someone standing on main in a repository with no remote yet.
func untrustedParent(plan Plan, unknownDefault bool) repair.Note {
	reason := fmt.Sprintf("%s is not in the g2g graph, so recording %s under it would make %s a trunk", plan.Parent, plan.Name, plan.Parent)
	ways := []repair.Step{
		{Command: "g2g adopt --branch " + repair.Quote(plan.Parent), Effect: "record the stack " + plan.Parent + " is on first"},
		{Effect: "pass --parent with a branch the graph records"},
	}
	if unknownDefault {
		reason = fmt.Sprintf("%s is not in the g2g graph and no default branch is known here, so recording %s under it would make %s a trunk", plan.Parent, plan.Name, plan.Parent)
		ways = append(ways, repair.Step{Command: "git remote set-head origin --auto", Effect: "if " + plan.Parent + " is origin's default branch, record that here · create then accepts it"})
	}
	// Nothing here can tell a trunk from a feature branch without evidence,
	// and the repository did not say. Recording a first branch by hand is the
	// user saying so, and after it create works here.
	ways = append(ways, repair.Step{Effect: "if " + plan.Parent + " is a trunk, start its first branch with git switch -c and record it with g2g track --parent " + plan.Parent})
	return repair.Note{Reason: reason, Ways: ways}
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
				{Command: "g2g create " + repair.Quote(plan.Name), Effect: "create the branch without committing"},
			},
		})
	}
	return plan
}

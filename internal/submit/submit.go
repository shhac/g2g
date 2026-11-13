package submit

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/pause"
	"github.com/shhac/g2g/internal/push"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
)

type Git interface {
	stack.Git
	Clean(context.Context) error
	// Remote is checked before anything is read, so a mistyped remote costs
	// nothing on GitHub. push checks it again when it plans; that is one
	// local read.
	Remote(context.Context, string) error
	IsAncestor(ctx context.Context, ancestor, descendant string) (bool, error)
	// ResolveAll says where each branch is, in one process: what the pull
	// requests a push updated are waited on to report.
	ResolveAll(ctx context.Context, revisions []string) (map[string]string, error)
}

// Pusher publishes the stack, and refuses a remote that holds work the stack
// does not have.
//
// It is push's own planning rather than a copy of its lease. submit used to
// build its leases from the tips it read and push them itself, and a lease
// pinned to the tip you just read always matches: a reviewer's commit sitting
// on the remote was force-pushed over, and then a pull request opened for
// what was left. push refuses exactly that, and land already went through it.
//
// It is handed the snapshot submit selected rather than a selection to make
// again: what submit leaves out of a fork is no scope push could be asked for.
type Pusher interface {
	PlanOf(ctx context.Context, snapshot stack.Snapshot, remote string, upstream localgit.Upstream) (push.Plan, error)
	Execute(ctx context.Context, plan push.Plan) error
}

type GitHub interface {
	stack.GitHub
	Create(context.Context, string, string, string, string, bool, []string) error
	Link(context.Context, string, []string) error
}

type Service struct {
	Git Git
	// Selector supplies the selection, forked or not, from whichever source
	// describes the branch.
	Selector stack.PathSelector
	GitHub   GitHub
	Pusher   Pusher
	// pause is the clock the wait after a push uses. Nil is the real one; a
	// test replaces it so the wait costs nothing.
	pause pause.Func
}

type Plan struct {
	Snapshot stack.Snapshot
	Remote   string
	Existing []githubstack.PullRequest
	Issues   map[string]string
	// Superseded records branches whose only pull requests are closed or
	// merged, so a preview can show that a new one will be created rather than
	// silently reusing a branch name that has history.
	Superseded map[string]githubstack.PullRequest
	// Push is how the stack is published, planned by push with its refusals.
	Push push.Plan
	// Waiting are the branches above the target left out of the submission,
	// each with what it sits on: each has no commits of its own yet and no
	// open pull request, which GitHub would refuse to open.
	Waiting map[string]string
}

// Ready reports a service with everything it needs.
//
// One rule, called by both the guard below and the command registration in
// internal/cli. They were two hand-written conjunctions before, and three of
// them had already drifted -- a command could be registered and then refuse on
// use, or be hidden from a build that could have run it.
func (s Service) Ready() bool {
	return s.Git != nil && s.Selector != nil && s.GitHub != nil && s.Pusher != nil
}

// Blocked is why an apply would refuse, empty when it would proceed.
func (p Plan) Blocked() string {
	if len(p.Issues) != 0 {
		return "existing pull request state: " + issueText(p.Issues)
	}
	return p.Push.Blocked()
}

// LinkBlocked keeps base agreement a requirement of explicit GitHub linking,
// rather than of publishing commits to pull requests with intentional bases.
func (p Plan) LinkBlocked() string { return p.LinkRepair().Sentence() }

// LinkRepair is why the pull requests cannot be linked, and the ways out. A
// GitHub native stack is one line, so a fork can be submitted and not linked.
func (p Plan) LinkRepair() repair.Note {
	if note := p.Snapshot.LinearRepair("linking"); note.Reason != "" {
		note.Ways = append(note.Ways, repair.Step{Effect: "drop --link to publish the whole fork, with each pull request based on its own parent"})
		return note
	}
	misbased := p.misbased()
	if len(misbased) == 0 {
		return repair.Note{}
	}
	step := misbased[0]
	return repair.Note{
		Reason: fmt.Sprintf("linking requires PR base %s for %s, found %s", step.ExpectedBase, step.Branch, step.Resolution.Open.Base),
		Ways: []repair.Step{
			{Command: p.retarget(), Effect: "align the PR bases with the selected stack"},
			{Effect: "drop --link to preserve existing bases"},
		},
	}
}

// misbased is each selected branch whose open pull request is based somewhere
// other than where the stack records it -- the branch it sits on, or the base.
func (p Plan) misbased() []githubstack.PathStep {
	misbased := make([]githubstack.PathStep, 0)
	for step := range p.Snapshot.Steps(p.Existing) {
		if step.Classify() == githubstack.StepBaseMismatch {
			misbased = append(misbased, step)
		}
	}
	return misbased
}

// OffStack is each misbased pull request whose base is not in the stack at
// all: not the base, not a branch on the way to it, not a selected branch.
// submit leaves an existing base alone, which is right for one somebody chose
// -- every pull request aimed at the trunk, say -- and wrong for one left on a
// branch the stack no longer has, after the branch below it was deleted.
func (p Plan) OffStack() []githubstack.PathStep {
	inStack := slices.Concat([]string{p.Snapshot.Base}, p.Snapshot.Ancestry, p.Snapshot.Branches)
	off := make([]githubstack.PathStep, 0)
	for _, step := range p.misbased() {
		if !slices.Contains(inStack, step.Resolution.Open.Base) {
			off = append(off, step)
		}
	}
	return off
}

// retarget is the command that moves every misbased pull request of this
// selection to where the stack records it, for a refusal this package words.
// The CLI aims its own suggestions; this one cannot reach that from here.
func (p Plan) retarget() string {
	scope := p.Snapshot.Scope
	if scope == "" {
		scope = stack.ScopeStack
	}
	command := fmt.Sprintf("g2g github retarget --branch %s --scope %s", repair.Quote(p.Snapshot.Target), scope)
	if p.Snapshot.Source != "" {
		command += " --from " + string(p.Snapshot.Source)
	}
	return command
}

// CreationBranches are the selected branches that need new PR metadata, in
// stack order. Closed and merged predecessors are replaced rather than reused.
func (p Plan) CreationBranches() []string {
	resolutions := githubstack.ResolveHeads(p.Existing)
	branches := make([]string, 0)
	for _, branch := range p.Snapshot.Branches {
		if resolutions[branch].Open == nil {
			branches = append(branches, branch)
		}
	}
	return branches
}

// ExistingSpec needs no new PR text: every selected branch already has an open
// pull request. Its empty entry list is sufficient and no metadata is sent.
func (p Plan) ExistingSpec() (Spec, bool) {
	if len(p.CreationBranches()) != 0 || len(p.Issues) != 0 {
		return Spec{}, false
	}
	resolutions := githubstack.ResolveHeads(p.Existing)
	for _, branch := range p.Snapshot.Branches {
		if resolutions[branch].OpenCount != 1 {
			return Spec{}, false
		}
	}
	return NewSpec(nil, ""), true
}

func (s Service) Plan(ctx context.Context, selection stack.Selection, remote string, upstream localgit.Upstream) (Plan, error) {
	if !s.Ready() {
		return Plan{}, fmt.Errorf("submit service is not fully configured")
	}
	if err := s.Git.Remote(ctx, remote); err != nil {
		return Plan{}, err
	}
	discovery, err := stack.Discover(ctx, s.Selector, s.GitHub, selection, "g2g submit")
	if err != nil {
		return Plan{}, err
	}
	// A fork is submitted whole: each missing pull request opens on the
	// branch it is recorded on, and the push is one atomic push whatever the
	// shape. Only linking needs a line, and LinkRepair refuses a fork.
	waiting, err := s.waiting(ctx, discovery.Snapshot, discovery.PullRequests)
	if err != nil {
		return Plan{}, err
	}
	snapshot := narrowed(discovery.Snapshot, waiting)
	issues, superseded := assessExisting(snapshot, discovery.PullRequests)
	published, err := s.Pusher.PlanOf(ctx, snapshot, remote, upstream)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{Snapshot: snapshot, Remote: remote, Existing: discovery.PullRequests, Issues: issues, Superseded: superseded, Push: published, Waiting: waiting}
	decision := "ready"
	if plan.Blocked() != "" {
		decision = "blocked"
	}
	diagnostic.Event(ctx, "submit.plan", diagnostic.Field{Key: "decision", Value: decision}, diagnostic.Field{Key: "target", Value: snapshot.Target}, diagnostic.Field{Key: "remote", Value: remote}, diagnostic.Field{Key: "branches", Value: strings.Join(snapshot.Branches, ",")})
	return plan, nil
}

// waiting is each branch above the target with nothing to submit, with what
// it sits on: no commits beyond its parent, no open pull request, and nothing
// above it that is submitted either. A branch just created to work in next is
// one, and taking it in asked for a title it could not use and a pull request
// GitHub refuses. The target itself is never left out, because naming it is
// asking, and neither is a branch something submitted sits on, because its
// pull request is what that one is based on.
//
// Each arm of a fork is asked on its own, from its top down, so an empty
// branch at the top of one arm is left out while the other arm is submitted.
func (s Service) waiting(ctx context.Context, snapshot stack.Snapshot, prs []githubstack.PullRequest) (map[string]string, error) {
	if !slices.Contains(snapshot.Branches, snapshot.Target) {
		return nil, nil
	}
	forest := shape.Forest{Parents: snapshot.Shape().Parents}
	above := forest.Subtree(snapshot.Target)[1:]
	resolutions := githubstack.ResolveHeads(prs)
	waiting := map[string]string{}
	// Reversed pre-order reaches every child before its parent.
	for _, branch := range slices.Backward(above) {
		if !allWaiting(forest.Children(branch), waiting) {
			continue
		}
		idle, err := s.idle(ctx, snapshot, resolutions[branch], branch)
		if err != nil {
			return nil, err
		}
		if idle {
			waiting[branch] = snapshot.SitsOn(branch)
		}
	}
	return waiting, nil
}

func allWaiting(branches []string, waiting map[string]string) bool {
	for _, branch := range branches {
		if _, left := waiting[branch]; !left {
			return false
		}
	}
	return true
}

// idle reports a branch with nothing to submit: no open pull request, and no
// commit beyond what it sits on. Two open pull requests resolve to none, and
// leaving that branch out would hide the ambiguity that blocks it.
func (s Service) idle(ctx context.Context, snapshot stack.Snapshot, resolution githubstack.Resolution, branch string) (bool, error) {
	if resolution.OpenCount != 0 {
		return false, nil
	}
	return s.Git.IsAncestor(ctx, branch, snapshot.SitsOn(branch))
}

// narrowed is the selection without the branches left out of it.
func narrowed(snapshot stack.Snapshot, left map[string]string) stack.Snapshot {
	if len(left) == 0 {
		return snapshot
	}
	parents := maps.Clone(snapshot.Parents)
	kept := make([]string, 0, len(snapshot.Branches))
	for _, branch := range snapshot.Branches {
		if _, out := left[branch]; out {
			delete(parents, branch)
			continue
		}
		kept = append(kept, branch)
	}
	snapshot.Branches, snapshot.Parents = kept, parents
	return snapshot
}

// Equal compares every fact that can change what the submission does.
func (p Plan) Equal(other Plan) bool {
	return p.Remote == other.Remote &&
		maps.Equal(p.Waiting, other.Waiting) &&
		p.Snapshot.Equal(other.Snapshot) &&
		githubstack.SettledEqual(p.Existing, other.Existing) &&
		maps.Equal(p.Issues, other.Issues) &&
		maps.EqualFunc(p.Superseded, other.Superseded, func(a, b githubstack.PullRequest) bool { return a.Settled() == b.Settled() }) &&
		p.Push.Equal(other.Push)
}

// assessExisting reports only what blocks submission. A branch whose pull
// requests are all closed or merged is not blocked: re-submitting a stack
// whose branch names were used before is the recovery this command exists for,
// so that history is recorded as superseded and a new pull request is created.
// Existing bases are preserved; only explicit linking requires agreement.
func assessExisting(snapshot stack.Snapshot, prs []githubstack.PullRequest) (map[string]string, map[string]githubstack.PullRequest) {
	issues := map[string]string{}
	superseded := map[string]githubstack.PullRequest{}
	for step := range snapshot.Steps(prs) {
		// A missing pull request is not an issue for submit: creating it is the
		// job. That is the whole of submit's policy difference from link.
		switch step.Classify() {
		case githubstack.StepAmbiguous:
			issues[step.Branch] = fmt.Sprintf("%d open pull requests", step.Resolution.OpenCount)
		case githubstack.StepSuperseded:
			superseded[step.Branch] = *step.Resolution.Latest
		}
	}
	return issues, superseded
}
func issueText(issues map[string]string) string {
	parts := make([]string, 0, len(issues))
	for branch, issue := range issues {
		parts = append(parts, branch+": "+issue)
	}
	sort.Strings(parts)
	return strings.Join(parts, "; ")
}

var _ Git = localgit.Client{}

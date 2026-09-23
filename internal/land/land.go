package land

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/landed"
	"github.com/shhac/g2g/internal/prune"
	"github.com/shhac/g2g/internal/push"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
	syncer "github.com/shhac/g2g/internal/sync"
)

// Git is the local half: what is here, what the remote has, and the two ref
// deletions landing performs.
type Git interface {
	stack.Git
	Clean(ctx context.Context) error
	Resolve(ctx context.Context, revision string) (string, error)
	IsAncestor(ctx context.Context, ancestor, descendant string) (bool, error)
	RemoteTips(ctx context.Context, remote string, branches []string) (map[string]string, error)
	FetchIsolated(ctx context.Context, remote string, branches []string) error
	Cherry(ctx context.Context, upstream, head, limit string) (absent, present []string, err error)
	Absorbed(ctx context.Context, base, branch string) (bool, error)
	DeleteBranch(ctx context.Context, branch string) error
	DeleteRemoteBranch(ctx context.Context, remote, branch string) error
	SwitchBranch(ctx context.Context, branch string) error
}

// GitHub is what landing asks of gh: who the pull requests are, what their
// merges would do, and the two mutations that land one.
type GitHub interface {
	Inspect(ctx context.Context, branches []string) ([]githubstack.PullRequest, error)
	Mergeability(ctx context.Context, numbers []int) (githubstack.Mergeability, error)
	Merge(ctx context.Context, number int, method githubstack.Method, admin bool) error
	Retarget(ctx context.Context, number int, base string) error
}

// The three services landing composes. Each is an interface rather than the
// service itself for the reason sync gives for the same choice: the job here is
// ordering, and ordering should be testable without standing up a rewrite
// engine or a network.
type (
	// Pusher publishes one branch and refuses a remote that has moved.
	Pusher interface {
		Plan(ctx context.Context, selection stack.Selection, remote string) (push.Plan, error)
		Execute(ctx context.Context, plan push.Plan) error
	}
	// Syncer advances the base and replays what is left onto it.
	Syncer interface {
		Plan(ctx context.Context, selection graph.Selection, remote string, take syncer.Take) (syncer.Plan, error)
		Apply(ctx context.Context, plan syncer.Plan) error
	}
	// Pruner forgets a branch whose work Git says is upstream.
	Pruner interface {
		Plan(ctx context.Context, selection graph.Selection) (prune.Plan, error)
		Apply(ctx context.Context, plan prune.Plan) error
	}
	// Holds says which of these branches another worktree has checked out.
	// It is restack's own check, asked here of everything a descent will move.
	Holds interface {
		HeldElsewhere(ctx context.Context, branches []string) (repair.Note, error)
	}
)

// Service takes a stack down onto its trunk.
type Service struct {
	Git      Git
	Graph    graph.Service
	Selector stack.PathSelector
	GitHub   GitHub
	Pusher   Pusher
	Syncer   Syncer
	Pruner   Pruner
	// Holds is optional: a build that cannot ask lands exactly as safely as it
	// did before the check existed.
	Holds Holds

	// pause is the clock the two waits use. Nil is the real one; a test
	// supplies its own so the suite does not spend the wall time.
	pause pauser
}

// Options are the choices a run was given.
type Options struct {
	Remote string
	Method githubstack.Method
	Admin  bool
	// The three cleanups, all on unless asked otherwise.
	DeleteRemote bool
	DeleteLocal  bool
	Forget       bool
	// Comment keeps the stack comments on what remains above the landed
	// branches once the descent is done, so the pull requests that merged
	// read as merged history there. On unless asked otherwise.
	Comment bool
}

// Defaults are the options a bare invocation means.
func Defaults() Options {
	return Options{Remote: "origin", Method: githubstack.MethodSquash, DeleteRemote: true, DeleteLocal: true, Forget: true, Comment: true}
}

// Plan is the whole descent, decided before any of it runs.
type Plan struct {
	stack.Discovery
	Options Options
	Trunk   string
	Steps   []Step
	// Above are the branches recorded on the last one landed, which are what
	// remains of the stack afterwards: each is the bottom of a stack on the
	// trunk once the descent is done.
	Above []string
	// Protected names the branches whose merge will need --admin once their
	// own restack has force-pushed them and restarted the required checks
	// that were green when this was planned. It is said in the preview
	// because discovering it at the second branch is discovering it after the
	// first has already merged.
	Protected []string
	Blocked   string
	Repair    repair.Note
}

// Nothing reports a plan with no branch left to land.
func (p Plan) Nothing() bool {
	for _, step := range p.Steps {
		if step.Merges() {
			return false
		}
	}
	return len(p.Steps) == 0 || !p.cleaning()
}

func (p Plan) cleaning() bool {
	return p.Options.Forget || p.Options.DeleteLocal || p.Options.DeleteRemote
}

// Landing counts the branches with a merge still to perform.
func (p Plan) Landing() int {
	landing := 0
	for _, step := range p.Steps {
		if step.Merges() {
			landing++
		}
	}
	return landing
}

// Equal compares every fact that changes what the descent does.
//
// The volatile readiness fields are deliberately absent: mergeable moves from
// UNKNOWN to CLEAN while GitHub computes it, and a revalidation that refused
// over that would refuse at random. Safety comes from re-deciding each branch
// in its own cycle, immediately before merging it, which is a stronger check
// than comparing a preview taken before anything moved.
func (p Plan) Equal(other Plan) bool {
	return p.Discovery.Equal(other.Discovery) &&
		p.Options == other.Options &&
		p.Trunk == other.Trunk &&
		p.Blocked == other.Blocked &&
		slices.Equal(p.Steps, other.Steps) &&
		slices.Equal(p.Above, other.Above)
}

// Ready reports a service with everything it needs.
//
// One rule, called by both the guard below and the command registration in
// internal/cli, which spelled the same six-way conjunction out by hand.
func (s Service) Ready() bool {
	return s.Git != nil && s.Selector != nil && s.GitHub != nil &&
		s.Pusher != nil && s.Syncer != nil && s.Pruner != nil
}

// Plan decides the whole descent without changing anything.
func (s Service) Plan(ctx context.Context, selection stack.Selection, options Options) (Plan, error) {
	if !s.Ready() {
		return Plan{}, fmt.Errorf("land service is not fully configured")
	}
	discovery, err := stack.Discover(ctx, s.Selector, s.GitHub, selection, "g2g land")
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{Discovery: discovery, Options: options, Trunk: discovery.Base}
	if sentence, note := s.blockedBefore(ctx, discovery, options); sentence != "" {
		plan.Blocked, plan.Repair = sentence, note
		return plan, nil
	}

	numbers := openNumbers(discovery)
	mergeability, err := s.GitHub.Mergeability(ctx, numbers)
	if err != nil {
		return Plan{}, err
	}
	if !mergeability.Allowed.Permits(options.Method) {
		plan.Repair = repair.Note{
			Reason: fmt.Sprintf("this repository does not allow %s merges", options.Method),
			Ways:   allowedWays(mergeability.Allowed),
		}
		plan.Blocked = plan.Repair.Sentence()
		return plan, nil
	}

	tips, err := s.Git.RemoteTips(ctx, options.Remote, discovery.Branches)
	if err != nil {
		return Plan{}, err
	}
	for step := range githubstack.Along(discovery.Base, discovery.Branches, discovery.PullRequests) {
		// Landing merges every branch into the trunk, in turn, so that is what
		// each pull request's base has to be by the time its turn comes -- not
		// the branch below it, which is where it correctly sits now and which
		// will not exist by then. Along answers the stacked question, which is
		// the right one for status and the wrong one for this.
		step.ExpectedBase = plan.Trunk
		landed := s.landed(ctx, step.Branch, plan.Trunk)
		state := stateFor(mergeability, step)
		tip, _ := s.Git.Resolve(ctx, step.Branch)
		decided, note := classify(facts{
			Step:    step,
			State:   state,
			Landed:  landed,
			Current: tip != "" && tips[step.Branch] == tip,
			Tip:     tip,
			Admin:   options.Admin,
		})
		if note.Reason != "" {
			// The whole descent is refused, so there is no partial one to
			// describe. Keeping the steps decided so far would draw a stack
			// missing its upper branches and offer a recipe covering some of
			// them, which reads as the plan rather than as a fragment of one.
			plan.Repair = note
			plan.Blocked = note.Sentence()
			plan.Steps = nil
			return plan, nil
		}
		decided.RemoteTip = tips[step.Branch]
		if len(plan.Steps) != 0 {
			// Everything above the bottom branch is replayed onto the advanced
			// trunk before its turn, which rewrites it, so it will need
			// publishing however current it looks now.
			decided.Push = true
		}
		plan.Steps = append(plan.Steps, decided)
	}
	if len(plan.Steps) != 0 {
		recorded, err := s.Graph.Store.Load(ctx)
		if err != nil {
			return Plan{}, err
		}
		plan.Above = recorded.Children(plan.Steps[len(plan.Steps)-1].Branch)
	}
	blocking := protectedAfterRestack(plan.Steps, mergeability)
	if !options.Admin {
		plan.Protected = blocking
	}
	if options.Admin {
		// Said where it will be needed, so the preview and the recipe show the
		// merge that will actually be asked for. Whether it is needed is decided
		// again when the branch's turn comes; this is the forecast.
		for index := range plan.Steps {
			if slices.Contains(blocking, plan.Steps[index].Branch) {
				plan.Steps[index].Admin = true
			}
		}
	}
	diagnostic.Event(ctx, "land.plan",
		diagnostic.Field{Key: "branches", Value: strings.Join(discovery.Branches, ",")},
		diagnostic.Field{Key: "landing", Value: fmt.Sprint(plan.Landing())},
		diagnostic.Field{Key: "blocked", Value: plan.Blocked},
	)
	return plan, nil
}

// blockedBefore is everything that refuses the whole descent before any of it
// is decided branch by branch.
//
// Each of these is somebody else's refusal, asked here so it arrives before the
// first merge rather than after it. A diverged trunk discovered half way down
// leaves a stack that has partly landed and cannot be replayed.
//
// It answers with a sentence as well as the structure behind it, because a
// refusal that reaches a plan from another one may carry only the sentence:
// sync sets Blocked straight from the restack it delegates to, with no Repair
// beside it. Reading the structure alone let exactly that refusal through.
func (s Service) blockedBefore(ctx context.Context, discovery stack.Discovery, options Options) (string, repair.Note) {
	if err := discovery.RequireLinear("land"); err != nil {
		return err.Error(), repair.Note{}
	}
	if err := discovery.RequireActionable("g2g land"); err != nil {
		return err.Error(), repair.Note{}
	}
	if len(discovery.Branches) == 0 {
		return "nothing is stacked here to land", repair.Note{}
	}
	// Landing reads pull requests from whichever source describes the stack and
	// then replays, reparents and forgets in g2g's own graph. Those are not the
	// same record. Told to act on a structure g2g has not adopted, it would
	// merge every pull request and then find nothing to replay and nothing to
	// forget -- a stack taken apart on GitHub and left untouched here.
	if discovery.Source != stack.SourceG2G {
		note := repair.Note{
			Reason: fmt.Sprintf("this stack is described by %s, and landing rewrites the branches above each merge in g2g's own graph", discovery.Source),
			Ways: []repair.Step{
				{Command: "g2g adopt", Effect: "adopt it, so there is a structure to replay against"},
			},
		}
		return note.Sentence(), note
	}
	if discovery.Target == discovery.Base {
		note := repair.Note{
			Reason: fmt.Sprintf("%s is a trunk, and landing it would merge every branch above it", discovery.Target),
			Ways:   []repair.Step{{Effect: "stand on the branch you mean to land, or name it with --branch"}},
		}
		return note.Sentence(), note
	}
	if err := s.Git.Clean(ctx); err != nil {
		return err.Error(), repair.Note{}
	}
	if held := s.heldElsewhere(ctx, discovery.Target); held.Reason != "" {
		return held.Sentence(), held
	}
	pushed, err := s.Pusher.Plan(ctx, stack.Selection{Branch: discovery.Target, Trunk: discovery.Base, Scope: shape.ScopeStack}, options.Remote)
	if err == nil && pushed.Blocked != "" {
		return pushed.Blocked, pushed.Repair
	}
	synced, err := s.Syncer.Plan(ctx, graph.Selection{Branch: discovery.Target, Scope: graph.ScopeStack}, options.Remote, syncer.TakeNothing)
	if err == nil && synced.Blocked != "" {
		return synced.Blocked, synced.Repair
	}
	return "", repair.Note{}
}

// heldElsewhere refuses a descent that would move a branch another worktree
// has checked out: the trunk it advances, the branches it merges and deletes,
// and those above it that it replays.
//
// Each step's own sync refuses the same thing, but only once there is
// something to move — after the first merge, which does not come back. Asking
// sync up front found nothing while the trunk was level, so a descent with the
// trunk open in another worktree merged its bottom branch and then stopped.
func (s Service) heldElsewhere(ctx context.Context, target string) repair.Note {
	if s.Holds == nil {
		return repair.Note{}
	}
	recorded, err := s.Graph.Store.Load(ctx)
	var moving []string
	if err == nil {
		moving, err = recorded.Shape().Stack(target)
	}
	if err == nil {
		var held repair.Note
		held, err = s.Holds.HeldElsewhere(ctx, moving)
		if err == nil && held.Reason != "" {
			// Narrowing the selection is no way out here: a descent moves the
			// whole stack whatever was selected, because the replay after each
			// merge takes everything above it.
			return repair.Note{Reason: held.Reason, Ways: []repair.Step{{Effect: "switch that worktree to another branch, or close it"}}}
		}
	}
	if err != nil {
		return repair.Note{Reason: "cannot tell whether another worktree has a branch this would move: " + err.Error()}
	}
	return repair.Note{}
}

// protectedAfterRestack names the branches that will read blocked by the time
// their turn comes.
//
// Every branch above the first is force-pushed by its own restack, which
// restarts the required checks that were green when this was planned. On a
// protected repository that is not an edge case, it is every run, and saying it
// at the start is the difference between choosing --admin and discovering it
// once something has already merged.
func protectedAfterRestack(steps []Step, mergeability githubstack.Mergeability) []string {
	protects := false
	for _, state := range mergeability.States {
		if state.StateStatus == githubstack.StatusBlocked || state.StateStatus == githubstack.StatusBehind {
			protects = true
		}
	}
	if !protects {
		return nil
	}
	after := make([]string, 0, len(steps))
	for index, step := range steps {
		if index != 0 && step.Merges() {
			after = append(after, step.Branch)
		}
	}
	return after
}

// landed asks Git whether a branch's work is already in its base.
//
// Cherry first because it is cheaper and answers the ordinary case; Absorbed
// because it is the only one that sees a squash merge, which is the commonest
// way a branch in a stack lands. An error from either is read as "no": an
// unrelated history is an answer, not a failure.
// It cannot fail. An unrelated history cannot be compared, which is an answer
// rather than a failure: a branch nothing can say has landed has not.
func (s Service) landed(ctx context.Context, branch, base string) bool {
	upstream, err := landed.Into(ctx, s.Git, base, branch, "")
	return err == nil && upstream
}

func openNumbers(discovery stack.Discovery) []int {
	numbers := make([]int, 0, len(discovery.Branches))
	for _, resolution := range githubstack.ResolveHeads(discovery.PullRequests) {
		if resolution.Open != nil {
			numbers = append(numbers, resolution.Open.Number)
		}
	}
	slices.Sort(numbers)
	return numbers
}

func stateFor(mergeability githubstack.Mergeability, step githubstack.PathStep) githubstack.MergeState {
	if open := step.Resolution.Open; open != nil {
		return mergeability.States[open.Number]
	}
	return githubstack.MergeState{}
}

func allowedWays(allowed githubstack.Allowed) []repair.Step {
	ways := make([]repair.Step, 0, len(githubstack.Methods))
	for _, method := range githubstack.Methods {
		if allowed.Permits(method) {
			ways = append(ways, repair.Step{Command: "g2g land --method " + string(method), Effect: "use the method this repository allows"})
		}
	}
	if len(ways) == 0 {
		ways = append(ways, repair.Step{Effect: "allow a merge method in the repository's settings"})
	}
	return ways
}

// Revalidate re-decides the descent immediately before it starts.
func (s Service) Revalidate(ctx context.Context, selection stack.Selection, options Options, preview Plan) (Plan, error) {
	plan, err := s.Plan(ctx, selection, options)
	if err != nil {
		return Plan{}, err
	}
	return plan, diagnostic.Revalidated(ctx, "land", "land plan", plan.Equal(preview))
}

var _ Git = localgit.Client{}

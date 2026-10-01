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
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/stack"
)

// Plan is the whole descent, decided before any of it runs.
type Plan struct {
	stack.Discovery
	Options Options
	Trunk   string
	// Declaration is set when the branch landed is a declared trunk going into
	// Trunk. See declared.go.
	Declaration graph.Declaration
	Steps       []Step
	// Above are the branches recorded on the last one landed, which are what
	// remains of the stack afterwards: each is the bottom of a stack on the
	// trunk once the descent is done.
	Above []string
	// Republish is everything above the last branch landed that was already
	// published, parents first, each with what the remote held when this was
	// planned. Every sync replays them onto the advanced trunk, and without a
	// push their pull requests go on showing commits built on a branch that
	// has merged and gone.
	Republish []Republish
	// Protected names the branches whose merge will need --admin once their
	// own restack has force-pushed them and restarted the required checks
	// that were green when this was planned. It is said in the preview
	// because discovering it at the second branch is discovering it after the
	// first has already merged.
	Protected []string
	Blocked   string
	Repair    repair.Note
	// KeepTrunk leaves a trunk held by another worktree in place. This is
	// possible only for one branch with nothing above it to replay.
	KeepTrunk bool
	// Detach moves this checkout onto the fetched merge before deleting its
	// current branch, since the local trunk is unavailable for checkout.
	Detach bool
}

// Republish is one branch above the descent, to publish once it is over.
type Republish struct {
	Branch string
	// RemoteTip is what the remote held when the descent was planned. A remote
	// holding anything else is carrying work this descent has not seen.
	RemoteTip string
}

// Nothing reports a plan with no branch left to land, or to tidy up after.
func (p Plan) Nothing() bool { return len(p.Steps) == 0 }

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
//
// A step's Admin is one of them. It is derived from readiness — whether the
// merge needs protection bypassed, which is exactly what required checks
// finishing changes — and is decided again at merge time. Comparing it refused
// an --admin descent whose checks passed between the preview and the apply.
func (p Plan) Equal(other Plan) bool {
	return p.Discovery.Equal(other.Discovery) &&
		p.Options == other.Options &&
		p.Trunk == other.Trunk &&
		p.Declaration == other.Declaration &&
		p.KeepTrunk == other.KeepTrunk && p.Detach == other.Detach &&
		p.Blocked == other.Blocked &&
		slices.EqualFunc(p.Steps, other.Steps, sameStep) &&
		slices.Equal(p.Above, other.Above) &&
		slices.Equal(p.Republish, other.Republish)
}

// sameStep compares everything about a step but how ready it is.
func sameStep(one, other Step) bool {
	one.Admin, other.Admin = false, false
	return one == other
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
	recorded, err := s.Graph.Store.Load(ctx)
	if err != nil {
		return Plan{}, err
	}
	plan.Declaration = declared(recorded, discovery)
	if plan.declared() && !options.MethodChosen {
		method, refused := declaredMethod(plan.Declaration)
		if refused.Reason != "" {
			return plan.refuse(refused), nil
		}
		options.Method = method
		plan.Options = options
	}
	if sentence, note := s.blockedBefore(ctx, &plan, recorded); sentence != "" {
		return plan.refusedAs(sentence, note), nil
	}

	mergeability, err := s.GitHub.Mergeability(ctx, openNumbers(discovery))
	if err != nil {
		return Plan{}, err
	}
	if !mergeability.Allowed.Permits(options.Method) {
		return plan.refuse(repair.Note{
			Reason: fmt.Sprintf("this repository does not allow %s merges", options.Method),
			Ways:   allowedWays(mergeability.Allowed),
		}), nil
	}
	steps, refused, err := s.decideSteps(ctx, discovery, options, mergeability)
	if err != nil {
		return Plan{}, err
	}
	if refused.Reason != "" {
		return plan.refuse(refused), nil
	}
	plan.Steps, plan.Protected = forecastAdmin(steps, protectedAfterRestack(steps, mergeability), options.Admin)
	if len(plan.Steps) != 0 {
		last := plan.Steps[len(plan.Steps)-1].Branch
		plan.Above = recorded.Children(last)
		if plan.Republish, err = s.republishing(ctx, recorded, last, options.Remote); err != nil {
			return Plan{}, err
		}
	}
	diagnostic.Event(ctx, "land.plan",
		diagnostic.Field{Key: "branches", Value: strings.Join(discovery.Branches, ",")},
		diagnostic.Field{Key: "landing", Value: fmt.Sprint(plan.Landing())},
		diagnostic.Field{Key: "blocked", Value: plan.Blocked},
	)
	return plan, nil
}

// refuse is the plan refused for note's reason, with no steps.
//
// A refused descent keeps none of the steps decided before the refusal:
// drawing a stack missing its upper branches, with a recipe covering some of
// them, reads as the plan rather than as a fragment of one.
func (p Plan) refuse(note repair.Note) Plan { return p.refusedAs(note.Sentence(), note) }

// refusedAs is refuse for a refusal that reached the plan as a sentence,
// possibly with no structure behind it; see blockedBefore.
func (p Plan) refusedAs(sentence string, note repair.Note) Plan {
	p.Blocked, p.Repair, p.Steps = sentence, note, nil
	return p
}

// decideSteps decides every branch of the descent in order, or the reason the
// whole descent is refused.
func (s Service) decideSteps(ctx context.Context, discovery stack.Discovery, options Options, mergeability githubstack.Mergeability) ([]Step, repair.Note, error) {
	tips, err := s.Git.RemoteTips(ctx, options.Remote, discovery.Branches)
	if err != nil {
		return nil, repair.Note{}, err
	}
	trunks, err := s.trunksToAsk(ctx, discovery, discovery.Base, options.Remote)
	if err != nil {
		return nil, repair.Note{}, err
	}
	steps := make([]Step, 0, len(discovery.Branches))
	for step := range githubstack.Along(discovery.Base, discovery.Branches, discovery.PullRequests) {
		// Landing merges every branch into the trunk, in turn, so that is what
		// each pull request's base has to be by the time its turn comes -- not
		// the branch below it, which is where it correctly sits now and which
		// will not exist by then. Along answers the stacked question, which is
		// the right one for status and the wrong one for this.
		step.ExpectedBase = discovery.Base
		tip, _ := s.Git.Resolve(ctx, step.Branch)
		decided, note := classify(facts{
			Step:    step,
			State:   stateFor(mergeability, step),
			Landed:  s.landedIn(ctx, step.Branch, trunks),
			Current: tip != "" && tips[step.Branch] == tip,
			Tip:     tip,
			Admin:   options.Admin,
		})
		if note.Reason != "" {
			return nil, note, nil
		}
		decided.RemoteTip = tips[step.Branch]
		// Everything above the bottom branch is replayed onto the advanced
		// trunk before its turn, which rewrites it, so it will need publishing
		// however current it looks now.
		decided.Push = decided.Push || len(steps) != 0
		steps = append(steps, decided)
	}
	return steps, repair.Note{}, nil
}

// forecastAdmin says which merges will need --admin once their own restack
// has restarted the checks. Without --admin that is a warning the preview
// carries (Protected); with it, the mark goes on each step where it will be
// needed, so the preview and the recipe show the merge that will actually be
// asked for. Whether it is needed is decided again when the branch's turn
// comes; this is the forecast.
func forecastAdmin(steps []Step, blocking []string, admin bool) ([]Step, []string) {
	if !admin {
		return steps, blocking
	}
	for index := range steps {
		if slices.Contains(blocking, steps[index].Branch) {
			steps[index].Admin = true
		}
	}
	return steps, nil
}

// republishing is what the syncs will replay above the last branch landed and
// is already on the remote: the subtree above it, which is what a sync of its
// stack takes. A branch never published is left unpublished, as push would
// leave it for anyone who had not asked.
func (s Service) republishing(ctx context.Context, recorded graph.Graph, last, remote string) ([]Republish, error) {
	above := recorded.Shape().Subtree(last)[1:]
	if len(above) == 0 {
		return nil, nil
	}
	tips, err := s.Git.RemoteTips(ctx, remote, above)
	if err != nil {
		return nil, err
	}
	// Publishing a branch pushes its path from the trunk, so one sitting on a
	// branch that was never published would publish that one too. It is left
	// out with everything above it, here, rather than refused once something
	// has already merged.
	republish := make([]Republish, 0, len(above))
	reachable := map[string]bool{last: true}
	for _, branch := range above {
		tip := tips[branch]
		if tip == "" || !reachable[recorded.Edges[branch].Parent] {
			continue
		}
		reachable[branch] = true
		republish = append(republish, Republish{Branch: branch, RemoteTip: tip})
	}
	return republish, nil
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

// landedIn is landed against any of the trunk's versions.
func (s Service) landedIn(ctx context.Context, branch string, trunks []string) bool {
	for _, trunk := range trunks {
		if s.landed(ctx, branch, trunk) {
			return true
		}
	}
	return false
}

// trunksToAsk is where a branch's work may already be: the trunk here, and —
// once any pull request in the stack has merged — the trunk as the remote has
// it, fetched into g2g's own refs.
//
// A colleague merging the bottom pull request in the browser is the ordinary
// way a descent starts with something already landed, and the trunk here does
// not have that merge until somebody pulls. Asked only of it, the branch read
// as merged on GitHub and not in the trunk, and land told the user to submit
// work that was already there. With nothing merged there is nothing the
// remote's trunk could add, so it is not fetched.
func (s Service) trunksToAsk(ctx context.Context, discovery stack.Discovery, trunk, remote string) ([]string, error) {
	merged := false
	for _, pr := range discovery.PullRequests {
		merged = merged || pr.State == "MERGED"
	}
	if !merged {
		return []string{trunk}, nil
	}
	if err := s.Git.FetchIsolated(ctx, remote, []string{trunk}); err != nil {
		return nil, err
	}
	return []string{trunk, localgit.IsolatedRef(remote, trunk)}, nil
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

package land

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
)

// world is main <- one <- two, both published, both with an open pull request
// sitting where a correctly stacked one sits: #41 on the trunk, #42 on one.
type world struct {
	service Service
	events  *events
	git     *fakeGit
	github  *fakeGitHub
	pusher  *fakePusher
	syncer  *fakeSyncer
	store   *memoryStore
}

func newWorld(t *testing.T) *world {
	t.Helper()
	seen := &events{}
	one := githubstack.PullRequest{Number: 41, URL: "https://example.test/41", Head: "synthetic-one", HeadOID: "one-tip", Base: "synthetic-main", State: "OPEN"}
	two := githubstack.PullRequest{Number: 42, URL: "https://example.test/42", Head: "synthetic-two", HeadOID: "two-tip", Base: "synthetic-one", State: "OPEN"}

	git := &fakeGit{
		events:  seen,
		current: "synthetic-two",
		local:   []string{"synthetic-main", "synthetic-one", "synthetic-two"},
		tips:    map[string]string{"synthetic-one": "one-tip", "synthetic-two": "two-tip"},
		objects: map[string]string{"synthetic-main": "main-tip", "synthetic-one": "one-tip", "synthetic-two": "two-tip"},
		ancestors: map[string][]string{
			"refs/g2g/remotes/origin/synthetic-main": {"merge-41", "merge-42"},
		},
		absorbed: map[string]bool{},
	}
	github := &fakeGitHub{
		events: seen,
		prs:    []githubstack.PullRequest{one, two},
		states: map[int]githubstack.MergeState{
			41: {Number: 41, HeadOID: "one-tip", Base: "synthetic-main", State: "OPEN", Mergeable: "MERGEABLE", StateStatus: "CLEAN"},
			42: {Number: 42, HeadOID: "two-tip", Base: "synthetic-one", State: "OPEN", Mergeable: "MERGEABLE", StateStatus: "CLEAN"},
		},
		allowed: githubstack.Allowed{Squash: true, Merge: true, Rebase: true},
	}
	store := &memoryStore{graph: graph.Graph{
		Edges: map[string]graph.Edge{
			"synthetic-one": {Parent: "synthetic-main", ForkPoint: "main-tip"},
			"synthetic-two": {Parent: "synthetic-one", ForkPoint: "one-tip"},
		},
		Trunks: []string{"synthetic-main"},
	}}
	pusher := &fakePusher{events: seen, git: git}
	sync := &fakeSyncer{events: seen}

	return &world{
		events: seen, git: git, github: github, pusher: pusher, syncer: sync, store: store,
		service: Service{
			Git:   git,
			Graph: graph.Service{Git: nil, Store: store},
			Selector: fakeSelector{snapshot: stack.Snapshot{
				Target:       "synthetic-two",
				TargetSource: "current Git branch",
				Ancestry:     []string{"synthetic-main", "synthetic-one", "synthetic-two"},
				Base:         "synthetic-main",
				Branches:     []string{"synthetic-one", "synthetic-two"},
				Scope:        shape.ScopeStack,
				Source:       stack.SourceG2G,
			}},
			GitHub: github,
			Pusher: pusher,
			Syncer: sync,
			Pruner: &fakePruner{events: seen, store: store},
			pause:  instant,
		},
	}
}

func (w *world) plan(t *testing.T, options Options) Plan {
	t.Helper()
	plan, err := w.service.Plan(context.Background(), stack.Selection{Scope: shape.ScopeStack}, options)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	return plan
}

// Nothing may merge until every branch has been found landable. Discovering the
// fourth is a draft after the first three have merged is not a refusal, it is a
// half-landed stack.
func TestPlanRefusesTheWholeDescentBeforeAnythingMerges(t *testing.T) {
	for name, arrange := range map[string]func(*world){
		"the remote has moved on a branch": func(w *world) {
			w.pusher.blocked = "the remote has moved on synthetic-two"
		},
		"the trunk has diverged": func(w *world) {
			w.syncer.blocked = "synthetic-main and its remote have each moved where the other has not"
		},
		"the working tree is dirty": func(w *world) {
			w.git.dirty = errors.New("working tree is not clean")
		},
		"a pull request is a draft": func(w *world) {
			state := w.github.states[42]
			state.Draft = true
			w.github.states[42] = state
		},
		"the repository forbids squash merges": func(w *world) {
			w.github.allowed = githubstack.Allowed{Merge: true}
		},
		"a branch is not approved": func(w *world) {
			state := w.github.states[41]
			state.Review = "REVIEW_REQUIRED"
			w.github.states[41] = state
		},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			arrange(w)

			plan := w.plan(t, Defaults())
			if plan.Blocked == "" {
				t.Fatalf("Plan() allowed a descent it should refuse: %+v", plan.Steps)
			}
			if err := w.service.Apply(context.Background(), plan); err == nil {
				t.Error("Apply() ran a blocked plan")
			}
			if merges := w.events.only("merge:"); len(merges) != 0 {
				t.Errorf("merged %v while refusing", merges)
			}
		})
	}
}

func TestPlanRefusesToLandFromTheTrunk(t *testing.T) {
	w := newWorld(t)
	w.service.Selector = fakeSelector{snapshot: stack.Snapshot{
		Target: "synthetic-main", Base: "synthetic-main",
		Branches: []string{"synthetic-main"}, Source: stack.SourceG2G,
	}}

	plan := w.plan(t, Defaults())
	if !strings.Contains(plan.Blocked, "is a trunk") {
		t.Errorf("Blocked = %q, want a refusal to land from the trunk", plan.Blocked)
	}
}

// Every branch merges into the trunk in turn, so a pull request correctly based
// on the branch below it is one that has to move before its turn comes.
func TestPlanAimsEveryBranchAtTheTrunk(t *testing.T) {
	plan := newWorld(t).plan(t, Defaults())

	if len(plan.Steps) != 2 {
		t.Fatalf("Steps = %+v, want one per branch", plan.Steps)
	}
	if plan.Steps[0].Retargets() {
		t.Errorf("the bottom branch was given a retarget: %+v", plan.Steps[0])
	}
	if !plan.Steps[1].Retargets() || plan.Steps[1].From != "synthetic-one" || plan.Steps[1].Base != "synthetic-main" {
		t.Errorf("Steps[1] = %+v, want a move from synthetic-one to synthetic-main", plan.Steps[1])
	}
}

func TestApplyLandsBottomUpAndTidiesAfterEachBranch(t *testing.T) {
	w := newWorld(t)
	plan := w.plan(t, Defaults())

	if err := w.service.Apply(context.Background(), plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	merges := w.events.only("merge:")
	if len(merges) != 2 || !strings.HasPrefix(merges[0], "merge:41") || !strings.HasPrefix(merges[1], "merge:42") {
		t.Fatalf("merges = %v, want 41 then 42", merges)
	}
	// The retarget of the upper pull request has to happen before its merge,
	// and after the branch it used to sit on has gone.
	for _, ordered := range [][2]string{
		{"merge:41:squash:admin=false", "retarget:42:synthetic-main"},
		{"retarget:42:synthetic-main", "merge:42:squash:admin=false"},
		{"merge:41:squash:admin=false", "sync:synthetic-two"},
		{"merge:41:squash:admin=false", "prune:synthetic-one"},
	} {
		if first, second := w.events.index(ordered[0]), w.events.index(ordered[1]); first < 0 || second < 0 || first > second {
			t.Errorf("want %q before %q, got %v", ordered[0], ordered[1], w.events.seen)
		}
	}
}

// Only the branch whose turn it is gets published. Pushing the whole stack
// after every merge restarts the checks on every branch above, which is the
// cost this exists to avoid.
func TestApplyPublishesOnlyTheBranchItIsAboutToMerge(t *testing.T) {
	w := newWorld(t)
	// Both branches have moved since they were last published.
	w.git.tips = map[string]string{"synthetic-one": "one-old", "synthetic-two": "two-old"}
	plan := w.plan(t, Defaults())

	if err := w.service.Apply(context.Background(), plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	pushes := w.events.only("push:")
	if len(pushes) != 2 || pushes[0] != "push:synthetic-one" || pushes[1] != "push:synthetic-two" {
		t.Fatalf("pushes = %v, want one branch at a time, in order", pushes)
	}
	if first, second := w.events.index("push:synthetic-two"), w.events.index("merge:41:squash:admin=false"); first < second {
		t.Error("the upper branch was published before the lower one had merged")
	}
}

// Forgetting a branch that still has something recorded under it is what prune
// refuses to do. Reparenting first leaves it with no children, so the refusal
// never applies -- and the child keeps its own fork point, which is what keeps
// its replay to its own commits.
func TestApplyReparentsChildrenBeforeForgettingTheBranchTheySatOn(t *testing.T) {
	w := newWorld(t)
	plan := w.plan(t, Defaults())

	if err := w.service.Apply(context.Background(), plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if index := w.events.index("prune:synthetic-one"); index < 0 {
		t.Fatalf("synthetic-one was never forgotten: %v", w.events.seen)
	}
	// Recorded before it was pruned: the child moved up to the trunk rather
	// than being left hanging from a branch that no longer exists.
	if parent := w.store.graph.Edges["synthetic-two"].Parent; parent != "synthetic-main" && parent != "" {
		t.Errorf("synthetic-two parent = %q, want the trunk", parent)
	}
	if fork := w.store.graph.Edges["synthetic-two"].ForkPoint; fork != "" && fork != "one-tip" {
		t.Errorf("synthetic-two fork point = %q, want its own kept", fork)
	}
}

// The merge is done and cannot be taken back. A branch that would not delete is
// untidiness, and reporting it as a failed land would misdescribe what happened
// and strand every branch above it.
func TestCleanupFailuresDoNotStopTheDescent(t *testing.T) {
	w := newWorld(t)
	w.git.deleteErr = errors.New("synthetic refusal to delete")
	plan := w.plan(t, Defaults())

	if err := w.service.Apply(context.Background(), plan); err != nil {
		t.Fatalf("Apply() error = %v, want cleanup failures not to stop it", err)
	}
	if merges := w.events.only("merge:"); len(merges) != 2 {
		t.Errorf("merges = %v, want both branches landed anyway", merges)
	}
}

// Landing the top of a stack ends standing on the branch about to be deleted,
// and git will not remove the checkout.
func TestApplyMovesOffTheBranchItIsAboutToDelete(t *testing.T) {
	w := newWorld(t)
	plan := w.plan(t, Defaults())

	if err := w.service.Apply(context.Background(), plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	switched, deleted := w.events.index("switch:synthetic-main"), w.events.index("delete-local:synthetic-two")
	if switched < 0 || deleted < 0 || switched > deleted {
		t.Errorf("want a switch away before deleting the checkout, got %v", w.events.seen)
	}
}

func TestEachCleanupCanBeTurnedOffOnItsOwn(t *testing.T) {
	for name, testCase := range map[string]struct {
		options Options
		absent  string
	}{
		"no remote delete": {options: Options{Remote: "origin", Method: githubstack.MethodSquash, DeleteLocal: true, Forget: true}, absent: "delete-remote:"},
		"no local delete":  {options: Options{Remote: "origin", Method: githubstack.MethodSquash, DeleteRemote: true, Forget: true}, absent: "delete-local:"},
		"no forget":        {options: Options{Remote: "origin", Method: githubstack.MethodSquash, DeleteRemote: true, DeleteLocal: true}, absent: "prune:"},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			plan := w.plan(t, testCase.options)
			if err := w.service.Apply(context.Background(), plan); err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			if got := w.events.only(testCase.absent); len(got) != 0 {
				t.Errorf("%s happened anyway: %v", testCase.absent, got)
			}
		})
	}
}

// A descent that stops part-way has to say what landed. The branches below the
// failure are merged and staying merged, and the caller is told while the
// command is failing.
func TestStoppedCarriesTheBranchesThatLanded(t *testing.T) {
	w := newWorld(t)
	plan := w.plan(t, Defaults())
	w.github.mergeErr = nil

	// The second merge fails; the first has already happened.
	original := w.github.states
	w.service.GitHub = &failingSecondMerge{fakeGitHub: w.github, failFor: 42, states: original}

	err := w.service.Apply(context.Background(), plan)

	var stopped *Stopped
	if !errors.As(err, &stopped) {
		t.Fatalf("Apply() error = %v, want a *Stopped", err)
	}
	if len(stopped.Landed) != 1 || stopped.Landed[0] != "synthetic-one" {
		t.Errorf("Landed = %v, want the one that merged", stopped.Landed)
	}
	if stopped.Branch != "synthetic-two" {
		t.Errorf("Branch = %q, want where it stopped", stopped.Branch)
	}
	if !strings.Contains(err.Error(), "synthetic-one") {
		t.Errorf("error %q does not say what landed", err)
	}
}

// A merge is done once GitHub accepts it. What fails after it -- here the
// replay of the branch above -- does not undo it, and reporting only whole
// cycles told someone whose branch had merged that nothing had.
func TestStoppedCountsAMergeWhoseTidyingFailed(t *testing.T) {
	w := newWorld(t)
	plan := w.plan(t, Defaults())
	w.syncer.blocked = "synthetic replay refusal"

	err := w.service.Apply(context.Background(), plan)

	var stopped *Stopped
	if !errors.As(err, &stopped) {
		t.Fatalf("Apply() error = %v, want a *Stopped", err)
	}
	if len(stopped.Landed) != 1 || stopped.Landed[0] != "synthetic-one" || stopped.Branch != "synthetic-one" {
		t.Errorf("Landed = %v at %q, want synthetic-one merged and stopped after it", stopped.Landed, stopped.Branch)
	}
	if !stopped.PartWay() {
		t.Error("PartWay() = false for a descent that merged a pull request")
	}
}

// Stopping before anything changed is not stopping part-way. It is a failure,
// and exiting as though something had landed misled whatever read the status.
func TestStoppedBeforeAnythingChangedIsNotPartWay(t *testing.T) {
	w := newWorld(t)
	plan := w.plan(t, Defaults())
	w.pusher.level = true
	w.github.mergeErr = errors.New("synthetic merge refusal")

	err := w.service.Apply(context.Background(), plan)

	var stopped *Stopped
	if !errors.As(err, &stopped) {
		t.Fatalf("Apply() error = %v, want a *Stopped", err)
	}
	if stopped.PartWay() {
		t.Errorf("PartWay() = true with nothing merged or tidied: %+v", stopped)
	}
}

type failingSecondMerge struct {
	*fakeGitHub
	failFor int
	states  map[int]githubstack.MergeState
}

func (f *failingSecondMerge) Merge(ctx context.Context, number int, method githubstack.Method, admin bool) error {
	if number == f.failFor {
		return errors.New("synthetic merge refusal")
	}
	return f.fakeGitHub.Merge(ctx, number, method, admin)
}

// The recipe a preview shows and the work an apply does are built from the same
// steps, so they cannot come to describe different things.
func TestCommandsDescribeTheStepsApplyWalks(t *testing.T) {
	w := newWorld(t)
	plan := w.plan(t, Defaults())

	commands := plan.Commands()
	joined := make([]string, 0, len(commands))
	for _, command := range commands {
		if command.Command == "" {
			t.Errorf("a recipe line has nothing to run: %+v", command)
		}
		joined = append(joined, command.Command)
	}
	recipe := strings.Join(joined, "\n")

	for _, want := range []string{
		"gh pr merge 41 --squash",
		"gh pr edit 42 --base synthetic-main",
		"gh pr merge 42 --squash",
		"g2g prune --branch synthetic-one --scope branch --apply",
		"git push origin --delete synthetic-one",
		"git branch -D synthetic-one",
	} {
		if !strings.Contains(recipe, want) {
			t.Errorf("recipe missing %q:\n%s", want, recipe)
		}
	}
	// Never this: gh's own --delete-branch removes the local branch too.
	if strings.Contains(recipe, "--delete-branch") {
		t.Errorf("recipe offers gh's --delete-branch:\n%s", recipe)
	}
	if first, second := strings.Index(recipe, "merge 41"), strings.Index(recipe, "merge 42"); first > second {
		t.Error("the recipe merges top-down")
	}
}

// Apply syncs after every branch, the last included, because that is what
// brings the trunk here onto the final merge. The recipe left the last one out,
// so following it by hand ended with the trunk behind its remote.
func TestTheRecipeSyncsAfterEveryBranchAsApplyDoes(t *testing.T) {
	w := newWorld(t)
	plan := w.plan(t, Defaults())
	recipe := 0
	for _, command := range plan.Commands() {
		if command.Command == "g2g sync --apply" {
			recipe++
		}
	}

	if err := w.service.Apply(context.Background(), plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if applied := len(w.events.only("sync:")); recipe != applied {
		t.Errorf("the recipe syncs %d times and Apply %d", recipe, applied)
	}
}

func TestCommandsFollowTheCleanupFlags(t *testing.T) {
	w := newWorld(t)
	plan := w.plan(t, Options{Remote: "origin", Method: githubstack.MethodRebase, Admin: true})

	recipe := ""
	for _, command := range plan.Commands() {
		recipe += command.Command + "\n"
	}
	for _, absent := range []string{"g2g prune", "git branch -D", "git push origin --delete"} {
		if strings.Contains(recipe, absent) {
			t.Errorf("recipe offers %q with every cleanup turned off:\n%s", absent, recipe)
		}
	}
	if !strings.Contains(recipe, "gh pr merge 41 --rebase") {
		t.Errorf("recipe does not carry the chosen method:\n%s", recipe)
	}
}

// "Waiting for GitHub" reads as propagation lag, and the answer to that is to
// wait longer — so a push that never reached the remote at all sends somebody
// to raise a timeout that was never the problem. This is what the first real
// trial hit: the branch above the merge was replayed, no push was attempted,
// and the run reported sixty-six failed attempts to observe a commit that had
// never left the machine.
func TestAWaitThatFailsSaysWhetherThePushEvenLanded(t *testing.T) {
	w := newWorld(t)
	// The remote is behind the local branch, so a push is planned — and the
	// fake push, like a lease that was refused, moves nothing. The remote
	// therefore still holds what the plan saw, so the moved-remote guard is
	// satisfied and the wait is genuinely reached.
	w.git.tips = map[string]string{"synthetic-one": "one-stale", "synthetic-two": "two-tip"}
	w.github.states[41] = githubstack.MergeState{
		Number: 41, HeadOID: "one-stale", Base: "synthetic-main",
		State: "OPEN", Mergeable: "MERGEABLE", StateStatus: "CLEAN",
	}
	w.pusher.silent = true
	plan := w.plan(t, Defaults())
	// The wait gives up the way the real one does: the budget runs out, and
	// the context everything was asked on is done. The diagnosis ran on that
	// context, so in production it never ran at all.
	budget, expire := context.WithCancel(context.Background())
	defer expire()
	w.service.pause = func(ctx context.Context, _ time.Duration) error {
		expire()
		return ctx.Err()
	}

	err := w.service.Apply(budget, plan)

	if err == nil {
		t.Fatal("Apply() error = nil, want a refusal")
	}
	for _, want := range []string{"not on origin", "the push did not take effect", shortID("one-stale")} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
	// And it must not read as a GitHub problem, which is the misdirection.
	if strings.Contains(err.Error(), "gave up waiting") {
		t.Errorf("error still reports a wait rather than the push: %v", err)
	}
}

// Landing reads pull requests from whichever source describes the stack, and
// then replays and forgets in g2g's own graph. Told to act on a structure g2g
// has not adopted, it would merge every pull request and then find nothing to
// replay and nothing to forget — a stack taken apart on GitHub and left whole
// here.
func TestLandRefusesAStackG2GHasNotAdopted(t *testing.T) {
	for _, source := range []stack.Source{stack.SourceGraphite, stack.SourcePullRequest} {
		t.Run(string(source), func(t *testing.T) {
			w := newWorld(t)
			snapshot := w.service.Selector.(fakeSelector).snapshot
			snapshot.Source = source
			w.service.Selector = fakeSelector{snapshot: snapshot}

			plan := w.plan(t, Defaults())

			if !strings.Contains(plan.Blocked, string(source)) {
				t.Errorf("Blocked = %q, want it to name the source that described the stack", plan.Blocked)
			}
			if !strings.Contains(plan.Repair.Sentence(), "g2g track --stack") {
				t.Errorf("Repair = %q, want it to name the way in", plan.Repair.Sentence())
			}
			if err := w.service.Apply(context.Background(), plan); err == nil {
				t.Error("Apply() ran a blocked plan")
			}
			if merges := w.events.only("merge:"); len(merges) != 0 {
				t.Errorf("merged %v from a structure it cannot replay", merges)
			}
		})
	}
}

// GitHub answers about the head it currently knows, which for a moment after a
// push is the one before it. The second trial saw both wrong answers: a stale
// UNKNOWN, and — worse — a stale CONFLICTING, which is plausible, actionable
// and would send someone to rebase a branch that was fine.
//
// The head is the reliable signal and mergeability is derived from it, so
// nothing may be read from a report about a commit this did not push.
func TestLandIgnoresMergeabilityReportedAgainstAnUnpushedHead(t *testing.T) {
	for _, stale := range []string{githubstack.MergeableConflicting, githubstack.MergeableUnknown} {
		t.Run(stale, func(t *testing.T) {
			w := newWorld(t)
			// The branch has moved on since it was last published, so this
			// cycle pushes it — and GitHub, for a moment afterwards, answers
			// about the head it had before, with a verdict about that one.
			w.git.tips["synthetic-one"] = "one-old"
			w.github.states[41] = githubstack.MergeState{
				Number: 41, HeadOID: "one-old", Base: "synthetic-main",
				State: "OPEN", Mergeable: stale, StateStatus: "CLEAN",
			}
			plan := w.plan(t, Defaults())
			w.github.settleOn = func() {
				w.github.states[41] = githubstack.MergeState{
					Number: 41, HeadOID: "one-tip", Base: "synthetic-main",
					State: "OPEN", Mergeable: "MERGEABLE", StateStatus: "CLEAN",
				}
			}

			if err := w.service.Apply(context.Background(), plan); err != nil {
				t.Fatalf("Apply() error = %v", err)
			}

			// It waited rather than acting, and merged only once the head it
			// pushed was the head GitHub reported.
			if merges := w.events.only("merge:41"); len(merges) != 1 {
				t.Errorf("merges of 41 = %v, want exactly one, after the head settled", merges)
			}
			if w.github.asked < 2 {
				t.Errorf("asked GitHub %d times, want it to have waited for the head to catch up", w.github.asked)
			}
		})
	}
}

// A reviewer pushing a fix onto a branch mid-descent is the one thing push
// cannot tell from land's own replay: both leave the remote holding commits the
// branch does not have, and push refuses both. What separates them is whether
// the remote still holds what the plan saw, and land is the only thing that
// knows, because it moved these refs itself.
//
// Untested until now, and it is the only protection there is: push's own
// refusal is deliberately not consulted inside the cycle, and its lease is
// pinned to the tips push itself reads — so without this the force-push would
// succeed over the reviewer's commit and land would then merge it.
func TestLandRefusesABranchTheRemoteMovedOnMidDescent(t *testing.T) {
	w := newWorld(t)
	plan := w.plan(t, Defaults())

	// Between planning and the cycle, somebody else pushes to the branch.
	w.git.tips["synthetic-one"] = "one-from-a-reviewer"

	err := w.service.Apply(context.Background(), plan)

	if err == nil {
		t.Fatal("Apply() error = nil, want a refusal")
	}
	for _, want := range []string{"has moved on synthetic-one", "work this descent has not seen"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
	if pushes := w.events.only("push:"); len(pushes) != 0 {
		t.Errorf("pushed %v over a remote that had moved", pushes)
	}
	if merges := w.events.only("merge:"); len(merges) != 0 {
		t.Errorf("merged %v after the remote moved", merges)
	}
}

// Landing forgets each branch as it lands, so the path from the trunk holds
// exactly the branch being published. More than one means an earlier cycle did
// not tidy up — and the extra branch has already merged, so pushing it would
// put it back on the remote.
func TestLandRefusesToRepublishABranchThatAlreadyLanded(t *testing.T) {
	w := newWorld(t)
	plan := w.plan(t, Defaults())
	w.pusher.extra = "synthetic-already-landed"

	err := w.service.Apply(context.Background(), plan)

	if err == nil {
		t.Fatal("Apply() error = nil, want a refusal")
	}
	if !strings.Contains(err.Error(), "already landed") {
		t.Errorf("error %q does not name the branch that should not be pushed", err)
	}
	if merges := w.events.only("merge:"); len(merges) != 0 {
		t.Errorf("merged %v despite an untidy path", merges)
	}
}

// --admin is decided for each branch when its turn comes, not when the descent
// is planned. Every branch above the first is force-pushed by its own replay,
// which restarts the required checks, so a branch that was clean at plan time
// is blocked at merge time -- the case --admin exists for. The recheck saw that
// and passed it, and the merge then went out without the flag.
func TestAdminIsPassedForABranchThatBecameBlockedAfterItsReplay(t *testing.T) {
	w := newWorld(t)
	options := Defaults()
	options.Admin = true
	plan := w.plan(t, options)
	if plan.Steps[1].Admin {
		t.Fatal("synthetic-two needs --admin at plan time already, so this does not test the change")
	}
	restarted := w.github.states[42]
	restarted.StateStatus = githubstack.StatusBlocked
	w.github.states[42] = restarted

	if err := w.service.Apply(context.Background(), plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if w.events.index("merge:42:squash:admin=true") < 0 {
		t.Errorf("#42 was blocked when its turn came and was merged without --admin: %v", w.events.only("merge:"))
	}
	if w.events.index("merge:41:squash:admin=false") < 0 {
		t.Errorf("#41 was clean and should not have bypassed anything: %v", w.events.only("merge:"))
	}
}

// With --admin on a protected repository the recipe says --admin where the
// merge will need it, rather than a merge GitHub would refuse if run by hand.
func TestTheRecipeForecastsAdminOnTheBranchesAReplayWillBlock(t *testing.T) {
	w := newWorld(t)
	blocked := w.github.states[41]
	blocked.StateStatus = githubstack.StatusBlocked
	w.github.states[41] = blocked
	options := Defaults()
	options.Admin = true

	plan := w.plan(t, options)

	if len(plan.Protected) != 0 {
		t.Errorf("Protected = %v, want no warning once --admin was given", plan.Protected)
	}
	var merges []string
	for _, command := range plan.Commands() {
		if strings.HasPrefix(command.Command, "gh pr merge") {
			merges = append(merges, command.Command)
		}
	}
	if want := "gh pr merge 42 --squash --admin"; len(merges) != 2 || merges[1] != want {
		t.Errorf("merges in the recipe = %v, want the second to be %q", merges, want)
	}
}

// The warning that says --admin will be needed before the first merge rather
// than after it. Its whole reason for existing is that discovering this at the
// second branch is discovering it once something has already landed — and it
// had never returned a non-empty list in any test, so the status check, the
// --admin short-circuit and the skip-the-bottom-branch filter were all
// unverified.
func TestProtectedNamesTheBranchesTheirReplayWillBlock(t *testing.T) {
	steps := []Step{{Branch: "synthetic-one"}, {Branch: "synthetic-two"}, {Branch: "synthetic-three", Landed: true}}
	blocked := githubstack.Mergeability{States: map[int]githubstack.MergeState{
		41: {StateStatus: githubstack.StatusBlocked},
	}}
	behind := githubstack.Mergeability{States: map[int]githubstack.MergeState{
		41: {StateStatus: githubstack.StatusBehind},
	}}
	clean := githubstack.Mergeability{States: map[int]githubstack.MergeState{
		41: {StateStatus: "CLEAN"},
	}}

	for name, testCase := range map[string]struct {
		mergeability githubstack.Mergeability
		want         string
	}{
		// The bottom branch is not named: it merges before anything is
		// replayed, so its checks are the ones that were already green.
		"blocked":     {blocked, "synthetic-two"},
		"behind":      {behind, "synthetic-two"},
		"unprotected": {clean, ""},
	} {
		t.Run(name, func(t *testing.T) {
			got := protectedAfterRestack(steps, testCase.mergeability)
			if strings.Join(got, ",") != testCase.want {
				t.Errorf("protectedAfterRestack() = %v, want %q", got, testCase.want)
			}
		})
	}
}

// Nothing merges while a branch the descent will move is open in another
// worktree. Each step's own sync refuses the same thing, but only once there
// is something to move — after the first merge, which does not come back.
func TestPlanRefusesADescentThatWouldMoveABranchOpenElsewhere(t *testing.T) {
	w := newWorld(t)
	holds := &fakeHolds{held: map[string]bool{"synthetic-main": true}}
	w.service.Holds = holds

	plan := w.plan(t, Defaults())
	if !strings.Contains(plan.Blocked, "synthetic-main") {
		t.Fatalf("Blocked = %q, want the trunk open elsewhere refused", plan.Blocked)
	}
	if !slices.Equal(holds.asked, []string{"synthetic-main", "synthetic-one", "synthetic-two"}) {
		t.Errorf("asked about %v, want the trunk and the whole stack", holds.asked)
	}
	for _, way := range plan.Repair.Ways {
		if strings.Contains(way.Effect, "--scope") {
			t.Errorf("way %q suggests narrowing, which a descent cannot use", way.Effect)
		}
	}
}

type fakeHolds struct {
	held  map[string]bool
	asked []string
}

func (f *fakeHolds) HeldElsewhere(_ context.Context, branches []string) (repair.Note, error) {
	f.asked = branches
	for _, branch := range branches {
		if f.held[branch] {
			return repair.Note{Reason: "checked out in another worktree: " + branch}, nil
		}
	}
	return repair.Note{}, nil
}

// A push is on the remote whatever happens after it. Refused at the merge, the
// descent has still changed something, and saying it failed as though nothing
// had told a script nothing had moved.
func TestAPublishBeforeARefusedMergeIsPartWay(t *testing.T) {
	w := newWorld(t)
	plan := w.plan(t, Defaults())
	w.github.mergeErr = errors.New("synthetic merge refusal")

	err := w.service.Apply(context.Background(), plan)

	var stopped *Stopped
	if !errors.As(err, &stopped) {
		t.Fatalf("Apply() error = %v, want a *Stopped", err)
	}
	if !stopped.PartWay() || !slices.Contains(stopped.Changed, "published synthetic-one") {
		t.Errorf("stopped = %+v, want the publish counted", stopped)
	}
}

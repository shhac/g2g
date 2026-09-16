package land

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/graph"
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
	pusher := &fakePusher{events: seen}
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
				Scope:        stack.ScopeStack,
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
	plan, err := w.service.Plan(context.Background(), stack.Selection{Scope: stack.ScopeStack}, options)
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

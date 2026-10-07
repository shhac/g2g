package land

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shhac/g2g/internal/githubstack"
)

// The syncs replay what sits above the landed branch, and a replay nobody
// publishes leaves its pull request showing commits built on a branch that has
// merged and gone. It is published once, after the descent, and said in the
// recipe before the comments are kept.
func TestApplyPublishesWhatItReplayedAboveTheDescent(t *testing.T) {
	w := newWorld(t)
	landingTheBottom(w)
	plan := w.plan(t, Defaults())
	if want := []Republish{{Branch: "synthetic-two", RemoteTip: "two-tip"}}; !slices.Equal(plan.Republish, want) {
		t.Fatalf("Republish = %+v, want %+v", plan.Republish, want)
	}
	commands := make([]string, 0)
	for _, command := range plan.Commands() {
		commands = append(commands, command.Command)
	}
	publish := slices.Index(commands, "g2g push --branch synthetic-two --scope path --apply")
	comment := slices.Index(commands, "g2g github comment --branch synthetic-two --apply")
	if publish < 0 || comment < publish || publish < slices.Index(commands, "git branch -D synthetic-one") {
		t.Errorf("recipe does not publish synthetic-two after the descent and before the comments:\n%s", strings.Join(commands, "\n"))
	}

	if err := w.service.Apply(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if got, want := w.events.only("push:"), []string{"push:synthetic-one", "push:synthetic-two"}; !slices.Equal(got, want) {
		t.Errorf("pushes = %v, want %v", got, want)
	}
	if w.events.index("push:synthetic-two") < w.events.index("sync:synthetic-one") {
		t.Errorf("synthetic-two was published before its replay: %v", w.events.seen)
	}
}

// A branch above that was never published stays that way: landing publishes
// what it replayed, not what nobody had asked to publish.
func TestApplyLeavesAnUnpublishedBranchAboveUnpublished(t *testing.T) {
	w := newWorld(t)
	landingTheBottom(w)
	delete(w.git.tips, "synthetic-two")
	plan := w.plan(t, Defaults())
	if len(plan.Republish) != 0 {
		t.Fatalf("Republish = %+v, want nothing", plan.Republish)
	}
	if err := w.service.Apply(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if got := w.events.only("push:synthetic-two"); len(got) != 0 {
		t.Errorf("published a branch that was never on the remote: %v", got)
	}
}

// Somebody pushed onto the branch above while the descent ran. Their work is
// not overwritten; the descent stands and says it stopped part-way.
func TestApplyStopsRatherThanOverwriteAReviewersPushAbove(t *testing.T) {
	w := newWorld(t)
	landingTheBottom(w)
	plan := w.plan(t, Defaults())
	w.git.tips["synthetic-two"] = "synthetic-reviewer-tip"

	err := w.service.Apply(context.Background(), plan)
	var stopped *Stopped
	if !errors.As(err, &stopped) || !stopped.PartWay() || stopped.Branch != "synthetic-two" || !slices.Equal(stopped.Landed, []string{"synthetic-one"}) {
		t.Fatalf("Apply() = %v, want a stop part-way at synthetic-two after landing synthetic-one", err)
	}
	if !strings.Contains(err.Error(), "has moved on synthetic-two") {
		t.Errorf("error = %v, want it to say the remote moved", err)
	}
	if got := w.events.only("push:synthetic-two"); len(got) != 0 {
		t.Errorf("pushed over the reviewer's work: %v", got)
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
		"no remote delete": {options: Options{Remote: "origin", Method: githubstack.MethodSquash, DeleteLocal: true}, absent: "delete-remote:"},
		"no local delete":  {options: Options{Remote: "origin", Method: githubstack.MethodSquash, DeleteRemote: true}, absent: "delete-local:"},
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

func (f *failingSecondMerge) Merge(ctx context.Context, number int, method githubstack.Method, admin bool, head string) error {
	if number == f.failFor {
		return errors.New("synthetic merge refusal")
	}
	return f.fakeGitHub.Merge(ctx, number, method, admin, head)
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

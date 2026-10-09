package land

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
	"github.com/shhac/g2g/internal/testutil"
)

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
		"a pull request is in a GitHub stack": func(w *world) {
			w.github.prs[1].StackNumber = 7
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
			if plan.Blocked() == "" {
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

// --strict asks the publish the descent would start with whether anything is
// out of step, and refuses before the first merge; without it, the same plan
// lands.
func TestStrictRefusesTheDescentOnlyWhenAsked(t *testing.T) {
	w := newWorld(t)
	w.pusher.strict = "--strict: it would drop synthetic-two 5a8d5c2"
	if plan := w.plan(t, Defaults()); plan.Blocked() != "" {
		t.Fatalf("Plan() without --strict refused: %s", plan.Blocked())
	}
	strict := Defaults()
	strict.Strict = true
	plan := w.plan(t, strict)
	if !strings.Contains(plan.Blocked(), "--strict") {
		t.Fatalf("Blocked = %q, want the strict refusal", plan.Blocked())
	}
	if err := w.service.Apply(context.Background(), plan); err == nil {
		t.Error("Apply() ran a strictly refused plan")
	}
	if merges := w.events.only("merge:"); len(merges) != 0 {
		t.Errorf("merged %v while refusing", merges)
	}
}

// GitHub will not merge a stacked pull request through gh pr merge, so a
// linked stack stopped at its first merge with nothing changed. The preview
// says so instead, and names the stack to unlink.
func TestPlanRefusesAStackLinkedOnGitHubAndNamesTheUnlink(t *testing.T) {
	w := newWorld(t)
	w.github.prs[0].StackNumber = 7
	w.github.prs[1].StackNumber = 7

	plan := w.plan(t, Defaults())
	if !strings.Contains(plan.Blocked(), "#41, #42 are in a GitHub stack") {
		t.Errorf("Blocked = %q, want it to name the stacked pull requests", plan.Blocked())
	}
	want := []repair.Step{{Command: "g2g github unlink --branch synthetic-two --stack-number 7", Effect: "unlink the GitHub stack, keeping its pull requests"}}
	if !slices.Equal(plan.Repair.Ways, want) {
		t.Errorf("Ways = %+v, want %+v", plan.Repair.Ways, want)
	}
	if len(plan.Steps) != 0 {
		t.Errorf("Steps = %+v, want none for a refused descent", plan.Steps)
	}
}

// A dirty tree is refused up front only where the descent moves the branch
// checked out here. A lone branch, or a stack whose replays apply cleanly,
// lands on GitHub and moves refs, and never needs the checkout.
func TestADirtyTreeIsRefusedOnlyWhereLandingTouchesIt(t *testing.T) {
	dirty := func(current string, lone bool) *world {
		w := newWorld(t)
		landingTheBottom(w)
		if lone {
			delete(w.store.graph.Edges, "synthetic-two")
		}
		w.git.current = current
		w.git.dirty = errors.New("working tree is not clean; commit or stash changes before --apply")
		return w
	}

	for _, w := range []*world{dirty("synthetic-elsewhere", true), dirty("synthetic-elsewhere", false)} {
		if plan := w.plan(t, Defaults()); plan.Blocked() != "" {
			t.Fatalf("Blocked = %q, want a descent that need not touch the checkout allowed beside unrelated work in progress", plan.Blocked())
		}
	}
	for name, arrange := range map[string]struct {
		world *world
		why   string
	}{
		"the branch landing is checked out": {dirty("synthetic-one", true), "synthetic-one is checked out here"},
		"the trunk is checked out":          {dirty("synthetic-main", true), "synthetic-main is checked out here"},
		"a branch above is checked out":     {dirty("synthetic-two", false), "synthetic-two is checked out here"},
	} {
		t.Run(name, func(t *testing.T) {
			if plan := arrange.world.plan(t, Defaults()); !strings.Contains(plan.Blocked(), arrange.why) {
				t.Errorf("Blocked = %q, want it refused saying %q", plan.Blocked(), arrange.why)
			}
		})
	}
}

// A replay that conflicts is resolved in the working tree, which here holds
// somebody's uncommitted work. The descent stops before replaying, with what
// merged standing, rather than mixing a conflict into their changes.
func TestADescentStopsBeforeAConflictingReplayInADirtyTree(t *testing.T) {
	w := newWorld(t)
	landingTheBottom(w)
	w.git.current = "synthetic-elsewhere"
	w.git.dirty = errors.New("working tree is not clean; commit or stash changes before --apply")
	w.syncer.conflicting = "synthetic-two"
	plan := w.plan(t, Defaults())
	if plan.Blocked() != "" {
		t.Fatalf("Blocked = %q, want the descent planned", plan.Blocked())
	}

	err := w.service.Apply(context.Background(), plan)
	var stopped *Stopped
	if !errors.As(err, &stopped) || !stopped.PartWay() || !slices.Equal(stopped.Landed, []string{"synthetic-one"}) {
		t.Fatalf("Apply() = %v, want a stop part-way after landing synthetic-one", err)
	}
	if !strings.Contains(err.Error(), "replaying synthetic-two onto synthetic-main conflicts") || !strings.Contains(err.Error(), "g2g pull --apply") {
		t.Errorf("error = %v, want it to name the replay and the way through", err)
	}
	if synced := w.events.only("sync:"); len(synced) != 0 {
		t.Errorf("replayed anyway: %v", synced)
	}

	// With nothing uncommitted, the same replay goes ahead: resolving it here
	// is the ordinary answer.
	w = newWorld(t)
	landingTheBottom(w)
	w.syncer.conflicting = "synthetic-two"
	if err := w.service.Apply(context.Background(), w.plan(t, Defaults())); err != nil {
		t.Fatalf("Apply() = %v, want a clean tree to take the conflicting replay", err)
	}
}

func TestPlanRefusesToLandFromTheTrunk(t *testing.T) {
	w := newWorld(t)
	w.service.Selector = fakeSelector{snapshot: stack.Snapshot{
		Target: "synthetic-main", Base: "synthetic-main",
		Branches: []string{"synthetic-main"}, Source: stack.SourceG2G,
	}}

	plan := w.plan(t, Defaults())
	if !strings.Contains(plan.Blocked(), "is a trunk") {
		t.Errorf("Blocked = %q, want a refusal to land from the trunk", plan.Blocked())
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

// A published branch above one that never was cannot be published without
// publishing that one too, since a push takes the path from the trunk. Finding
// that out after the first merge stopped the descent part-way; the plan leaves
// both out instead.
func TestPlanLeavesOutWhatSitsOnAnUnpublishedBranchAbove(t *testing.T) {
	w := newWorld(t)
	landingTheBottom(w)
	w.store.graph.Edges["synthetic-three"] = graph.Edge{Parent: "synthetic-two", ForkPoint: "two-tip"}
	w.git.local = append(w.git.local, "synthetic-three")
	w.git.objects["synthetic-three"] = "three-tip"
	w.git.tips["synthetic-three"] = "three-tip"
	delete(w.git.tips, "synthetic-two")

	plan := w.plan(t, Defaults())
	if len(plan.Republish) != 0 {
		t.Fatalf("Republish = %+v, want nothing above an unpublished branch", plan.Republish)
	}
	if err := w.service.Apply(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if got := w.events.only("push:"); !slices.Equal(got, []string{"push:synthetic-one"}) {
		t.Errorf("pushes = %v, want only the branch landed", got)
	}
}

// A reviewer pushing above between the preview and the apply is refused before
// anything merges, not found by the publish at the end.
func TestRevalidationRefusesAMoveAboveTheDescent(t *testing.T) {
	w := newWorld(t)
	landingTheBottom(w)
	preview := w.plan(t, Defaults())
	w.git.tips["synthetic-two"] = "synthetic-reviewer-tip"

	if _, err := testutil.Replan(preview)(w.service.Plan(context.Background(), stack.Selection{Scope: shape.ScopeStack}, Defaults())); !errors.Is(err, testutil.ErrReplanned) {
		t.Fatal("Replan() = nil, want the moved branch above to refuse the descent")
	}
	if merges := w.events.only("merge:"); len(merges) != 0 {
		t.Errorf("merged %v", merges)
	}
}

// Landing reads pull requests from whichever source describes the stack, and
// then replays and forgets in g2g's own graph. Told to act on a structure g2g
// has not adopted, it would merge every pull request and then find nothing to
// replay and nothing to forget — a stack taken apart on GitHub and left whole
// here.
func TestLandRefusesAStackG2GHasNotAdopted(t *testing.T) {
	for _, source := range []stack.Source{stack.SourceGraphite, stack.SourceGitHub} {
		t.Run(string(source), func(t *testing.T) {
			w := newWorld(t)
			snapshot := w.service.Selector.(fakeSelector).snapshot
			snapshot.Source = source
			w.service.Selector = fakeSelector{snapshot: snapshot}

			plan := w.plan(t, Defaults())

			if !strings.Contains(plan.Blocked(), string(source)) {
				t.Errorf("Blocked = %q, want it to name the source that described the stack", plan.Blocked())
			}
			if !strings.Contains(plan.Repair.Sentence(), "g2g adopt") {
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
	if !strings.Contains(plan.Blocked(), "synthetic-main") {
		t.Fatalf("Blocked = %q, want the trunk open elsewhere refused", plan.Blocked())
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

func TestALeafWithSurvivorsStillRefusesATrunkHeldElsewhere(t *testing.T) {
	w := newWorld(t)
	snapshot := w.service.Selector.(fakeSelector).snapshot
	snapshot.Target = "synthetic-one"
	snapshot.Branches = []string{"synthetic-one"}
	snapshot.Ancestry = []string{"synthetic-main", "synthetic-one"}
	snapshot.Scope = shape.ScopePath
	w.service.Selector = fakeSelector{snapshot: snapshot}
	w.service.Holds = &fakeHolds{held: map[string]bool{"synthetic-main": true}}
	plan := w.plan(t, Defaults())
	if plan.Blocked() == "" || plan.KeepTrunk {
		t.Fatalf("plan = %+v, want survivors protected", plan)
	}
}

func TestKeepingTheTrunkStillRefusesTheLeafHeldElsewhere(t *testing.T) {
	w := newWorld(t)
	w.store.graph = w.store.graph.Untrack("synthetic-two")
	snapshot := w.service.Selector.(fakeSelector).snapshot
	snapshot.Target = "synthetic-one"
	snapshot.Branches = []string{"synthetic-one"}
	w.service.Selector = fakeSelector{snapshot: snapshot}
	w.service.Holds = &fakeHolds{held: map[string]bool{"synthetic-main": true, "synthetic-one": true}}
	plan := w.plan(t, Defaults())
	if plan.Blocked() == "" || plan.KeepTrunk {
		t.Fatalf("plan = %+v, want held leaf protected", plan)
	}
}

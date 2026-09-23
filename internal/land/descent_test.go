package land

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
)

// threeHigh is the world with synthetic-three on top of synthetic-two, as #43.
func threeHigh(t *testing.T) *world {
	t.Helper()
	w := newWorld(t)
	w.git.local = append(w.git.local, "synthetic-three")
	w.git.tips["synthetic-three"] = "three-tip"
	w.git.objects["synthetic-three"] = "three-tip"
	w.git.ancestors["refs/g2g/remotes/origin/synthetic-main"] = append(w.git.ancestors["refs/g2g/remotes/origin/synthetic-main"], "merge-43")
	three := githubstack.PullRequest{Number: 43, URL: "https://example.test/43", Head: "synthetic-three", HeadOID: "three-tip", Base: "synthetic-two", State: "OPEN"}
	w.github.prs = append(w.github.prs, three)
	w.github.states[43] = githubstack.MergeState{Number: 43, HeadOID: "three-tip", Base: "synthetic-two", State: "OPEN", Mergeable: "MERGEABLE", StateStatus: "CLEAN"}
	w.store.graph.Edges["synthetic-three"] = graph.Edge{Parent: "synthetic-two", ForkPoint: "two-tip"}
	selector := w.service.Selector.(fakeSelector)
	selector.snapshot.Target = "synthetic-three"
	selector.snapshot.Ancestry = append(selector.snapshot.Ancestry, "synthetic-three")
	selector.snapshot.Branches = append(selector.snapshot.Branches, "synthetic-three")
	w.service.Selector = selector
	w.git.current = "synthetic-three"
	return w
}

// What reaches the remote stays linear however many branches the descent
// takes down: each merge publishes exactly the branch whose turn it is, asking
// push for that one path and nothing wider, and retargets exactly its one pull
// request. The replay after each merge is local; pushing the replayed
// branches above as it went would make the remote quadratic too.
func TestEachMergeReachesTheRemoteForItsOwnBranchAlone(t *testing.T) {
	w := threeHigh(t)
	// Every branch has moved since it was published, so each has a push due.
	w.git.tips = map[string]string{"synthetic-one": "one-old", "synthetic-two": "two-old", "synthetic-three": "three-old"}
	plan := w.plan(t, Defaults())
	// Planning asks push about the whole stack, to refuse up front; that is a
	// read. What matters is what the descent asks for when it publishes.
	w.pusher.scopes = nil

	if err := w.service.Apply(context.Background(), plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if got := w.events.only("push:"); !slices.Equal(got, []string{"push:synthetic-one", "push:synthetic-two", "push:synthetic-three"}) {
		t.Errorf("pushes = %v, want each branch once, alone, on its turn", got)
	}
	for _, scope := range w.pusher.scopes {
		if scope != shape.ScopePath {
			t.Errorf("a publish asked push for %s, want the path to the one branch", scope)
		}
	}
	if got := w.events.only("retarget:"); !slices.Equal(got, []string{"retarget:42:synthetic-main", "retarget:43:synthetic-main"}) {
		t.Errorf("retargets = %v, want each pull request above the first once", got)
	}
	if got := w.events.only("merge:"); len(got) != 3 {
		t.Errorf("merges = %v, want three", got)
	}
	// Each push precedes the merge it is for and follows the merge below it.
	for _, ordered := range [][2]string{
		{"merge:41:squash:admin=false", "push:synthetic-two"},
		{"merge:42:squash:admin=false", "push:synthetic-three"},
	} {
		if first, second := w.events.index(ordered[0]), w.events.index(ordered[1]); first < 0 || second < 0 || first > second {
			t.Errorf("want %q before %q, got %v", ordered[0], ordered[1], w.events.seen)
		}
	}
}

// A branch already in the trunk by content — merged by somebody else, or by an
// earlier run that stopped — is tidied and never merged again. That is all the
// re-entrancy land has: it is not journaled, it recomputes.
func TestABranchAlreadyLandedIsTidiedAndNotMergedAgain(t *testing.T) {
	w := newWorld(t)
	w.git.absorbed["synthetic-one"] = true
	w.github.prs[0].State = "MERGED"
	w.github.states[41] = githubstack.MergeState{Number: 41, HeadOID: "one-tip", Base: "synthetic-main", State: "MERGED", MergeCommit: "merge-41"}
	plan := w.plan(t, Defaults())

	if err := w.service.Apply(context.Background(), plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if got := w.events.only("merge:"); len(got) != 1 || !strings.HasPrefix(got[0], "merge:42") {
		t.Errorf("merges = %v, want only #42", got)
	}
	for _, want := range []string{"prune:synthetic-one", "delete-local:synthetic-one"} {
		if w.events.index(want) < 0 {
			t.Errorf("%s did not happen: %v", want, w.events.seen)
		}
	}
}

// A run that stops after tidying a branch an earlier run merged says so: that
// cleanup is not coming back either, so it is part of what stopped part-way.
func TestAStopAfterTidyingSaysWhatWasTidied(t *testing.T) {
	w := newWorld(t)
	w.git.absorbed["synthetic-one"] = true
	w.github.prs[0].State = "MERGED"
	w.github.states[41] = githubstack.MergeState{Number: 41, HeadOID: "one-tip", Base: "synthetic-main", State: "MERGED", MergeCommit: "merge-41"}
	w.github.mergeErr = errors.New("synthetic merge refusal")
	plan := w.plan(t, Defaults())

	err := w.service.Apply(context.Background(), plan)
	var stopped *Stopped
	if !errors.As(err, &stopped) || !slices.Contains(stopped.Tidied, "synthetic-one") || !stopped.PartWay() {
		t.Fatalf("Apply() error = %#v, want a stop part-way naming synthetic-one as tidied", err)
	}
}

// The next branch can stop being mergeable between its turn being planned and
// arriving — a colleague turns it back to draft, a conflict appears. It is
// decided again at merge time, and the merge is not attempted.
func TestABranchThatStoppedBeingMergeableIsNotMerged(t *testing.T) {
	w := newWorld(t)
	w.github.afterMerge = func(number int) {
		if number == 41 {
			state := w.github.states[42]
			state.Draft = true
			w.github.states[42] = state
		}
	}
	plan := w.plan(t, Defaults())

	err := w.service.Apply(context.Background(), plan)
	var stopped *Stopped
	if !errors.As(err, &stopped) || stopped.Branch != "synthetic-two" || !stopped.PartWay() {
		t.Fatalf("Apply() error = %#v, want a stop part-way at synthetic-two", err)
	}
	if got := w.events.only("merge:"); len(got) != 1 {
		t.Errorf("merges = %v, want only #41", got)
	}
}

// A merged branch prune will not forget stops the descent before its refs are
// deleted. Carrying on deleted the branch and left the graph recording it.
func TestAMergedBranchThatCannotBeForgottenKeepsItsRefs(t *testing.T) {
	for name, pruner := range map[string]fakePruner{
		"prune refuses":     {blocked: "synthetic refusal"},
		"not landed by git": {nothing: true},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			configured := w.service.Pruner.(*fakePruner)
			configured.blocked, configured.nothing = pruner.blocked, pruner.nothing
			plan := w.plan(t, Defaults())

			err := w.service.Apply(context.Background(), plan)
			var stopped *Stopped
			if !errors.As(err, &stopped) || !slices.Contains(stopped.Landed, "synthetic-one") {
				t.Fatalf("Apply() error = %#v, want a stop naming synthetic-one as landed", err)
			}
			for _, removed := range []string{"delete-local:synthetic-one", "delete-remote:synthetic-one"} {
				if w.events.index(removed) >= 0 {
					t.Errorf("%s happened for a branch the graph still records", removed)
				}
			}
			if !w.store.graph.Tracked("synthetic-one") {
				t.Error("synthetic-one was forgotten")
			}
		})
	}
}

// A push the lease refuses before anything merged changed nothing, so it is an
// ordinary failure rather than a stop part-way.
func TestARefusedFirstPushChangesNothing(t *testing.T) {
	w := newWorld(t)
	w.git.tips = map[string]string{"synthetic-one": "one-old", "synthetic-two": "two-old"}
	w.pusher.executeErr = errors.New("synthetic stale lease")
	plan := w.plan(t, Defaults())

	err := w.service.Apply(context.Background(), plan)
	var stopped *Stopped
	if !errors.As(err, &stopped) || stopped.PartWay() || len(w.events.only("merge:")) != 0 {
		t.Fatalf("Apply() error = %#v, events %v; want a stop that changed nothing", err, w.events.seen)
	}
}

// Revalidation refuses what changes the descent and not what changes how ready
// it is. A pull request whose required checks pass between the preview and the
// apply no longer needs protection bypassed, which is readiness; a branch the
// remote moved on is not.
func TestRevalidationRefusesStructureAndNotReadiness(t *testing.T) {
	options := Defaults()
	options.Admin = true
	selection := stack.Selection{Scope: shape.ScopeStack}

	w := newWorld(t)
	w.github.states[41] = githubstack.MergeState{Number: 41, HeadOID: "one-tip", Base: "synthetic-main", State: "OPEN", Mergeable: "MERGEABLE", StateStatus: "BLOCKED"}
	preview := w.plan(t, options)
	if !preview.Steps[0].Admin {
		t.Fatalf("preview step = %+v, want a merge that needs protection bypassed", preview.Steps[0])
	}
	w.github.states[41] = githubstack.MergeState{Number: 41, HeadOID: "one-tip", Base: "synthetic-main", State: "OPEN", Mergeable: "MERGEABLE", StateStatus: "CLEAN"}
	if _, err := w.service.Revalidate(context.Background(), selection, options, preview); err != nil {
		t.Errorf("Revalidate() refused checks passing: %v", err)
	}

	w.git.tips["synthetic-two"] = "two-reviewed"
	if _, err := w.service.Revalidate(context.Background(), selection, options, preview); err == nil {
		t.Error("Revalidate() accepted a branch the remote moved on since the preview")
	}
}

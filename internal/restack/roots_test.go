package restack

import (
	"context"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/graph"
)

// Two stacks on one trunk fork from it at different points. Replaying both
// from the first root's fork point widened the second's range to take in the
// trunk's own commits, so each root has to be its own replay, from its own
// fork point, onto its own base.
func TestEachRootIsReplayedFromItsOwnForkPoint(t *testing.T) {
	git := forestGit()
	service, _, _ := newService(git, forest())
	plan, err := service.Plan(context.Background(), selection(), Onto{}, false, nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := service.Apply(context.Background(), plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if len(git.replays) != 2 {
		t.Fatalf("replays = %v, want one per root", git.replays)
	}
	for index, want := range []struct{ from, targets string }{
		{"trunk-old", "synthetic-a,synthetic-b"},
		{"trunk-mid", "synthetic-x"},
	} {
		batch := git.replays[index]
		if got := strings.Join(rangeTargets(batch), ","); got != want.targets {
			t.Errorf("replay %d rewrote %s, want %s", index, got, want.targets)
		}
		for _, replayed := range batch {
			if replayed.From != want.from {
				t.Errorf("range %v starts at %q, want its own root's fork point %q", replayed, replayed.From, want.from)
			}
		}
	}
	// Previewed the same way it is applied, or the prediction is about a
	// rewrite that will not happen.
	if got := strings.Join(git.ontos[:2], ","); got != "trunk-new,trunk-new" {
		t.Errorf("previews landed on %s, want each root's base", got)
	}
}

// Several replays are several invocations, and the engine's atomicity covers
// one. A failure in the second left the first root rewritten, and the command
// said "Not applied" over refs that had moved.
func TestAFailedReplayPutsBackEveryRootItHadMoved(t *testing.T) {
	git := forestGit()
	git.replayFails = "synthetic-x"
	service, _, journal := newService(git, forest())
	plan, err := service.Plan(context.Background(), selection(), Onto{}, false, nil)
	if err != nil {
		t.Fatal(err)
	}

	err = service.Apply(context.Background(), plan)
	if err == nil {
		t.Fatal("Apply() error = nil after a replay failed")
	}
	if !strings.Contains(err.Error(), "put back") {
		t.Errorf("error = %v, want it to say the branches were put back", err)
	}
	for branch, tip := range map[string]string{"synthetic-a": "a-old", "synthetic-b": "b-old"} {
		if got, _ := git.Resolve(context.Background(), branch); got != tip {
			t.Errorf("%s = %s after the failure, want it put back at %s", branch, got, tip)
		}
	}
	// Nothing moved, so nothing is in progress: a journal left behind would
	// make every other command refuse over a repository that is untouched.
	if journal.present {
		t.Error("a rewrite that was put back left a journal behind")
	}
	if git.resets != 0 {
		t.Errorf("checkout reconciled %d times for a rewrite that was put back", git.resets)
	}
}

// Moving only the first of several roots is what --onto used to do, silently.
func TestOntoRefusesASelectionWithSeveralRoots(t *testing.T) {
	git := forestGit()
	git.objects["synthetic-release"] = "release-tip"
	service, _, _ := newService(git, forest())

	plan, err := service.Plan(context.Background(), selection(), ToBranch("synthetic-release"), false, nil)
	if err != nil {
		t.Fatal(err)
	}

	if plan.Blocked == "" {
		t.Fatal("Blocked is empty for an --onto over two roots")
	}
	var commands []string
	for _, way := range plan.Repair.Ways {
		commands = append(commands, way.Command)
	}
	want := "g2g restack --branch synthetic-a --onto synthetic-release,g2g restack --branch synthetic-x --onto synthetic-release"
	if got := strings.Join(commands, ","); got != want {
		t.Errorf("ways out = %s, want one per root", got)
	}
}

// A caller's location is where every root lands, not only the first. Taking
// the first alone left the other stacks measured against a trunk that was
// about to move.
func TestALocationMovesEveryRoot(t *testing.T) {
	git := forestGit()
	git.objects["synthetic-trunk"] = "trunk-old"
	git.ancestors["synthetic-x"] = []string{"trunk-old"}
	adopted := forest()
	adopted.Edges["synthetic-x"] = graph.Edge{Parent: "synthetic-trunk", ForkPoint: "trunk-old"}
	git.objects["refs/synthetic/fetched"] = "trunk-fetched"
	service, _, _ := newService(git, adopted)

	plan, err := service.Plan(context.Background(), selection(), ToLocation("refs/synthetic/fetched"), false, nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, step := range plan.Steps {
		if step.Branch == "synthetic-b" {
			continue
		}
		if step.Base != "trunk-fetched" {
			t.Errorf("%s lands on %s, want the location", step.Branch, step.Base)
		}
	}
	if got := strings.Join(plan.Branches(), ","); got != "synthetic-a,synthetic-b,synthetic-x" {
		t.Errorf("Branches() = %s, want both stacks", got)
	}
}

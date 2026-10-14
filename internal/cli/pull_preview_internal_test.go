package cli

import (
	"testing"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/restack"
)

// A branch whose work is already in its new base is moved there rather than
// replayed, which still moves a ref. The preview said "Nothing needs
// replaying" over a stack whose every branch the apply then moved.
func TestPullPreviewNamesTheBranchesItMoves(t *testing.T) {
	recorded := graph.New()
	for _, branch := range []string{"synthetic-a", "synthetic-b"} {
		updated, err := recorded.Track(branch, graph.Edge{Parent: "synthetic-main", ForkPoint: "0000000000000000000000000000000000000000"})
		if err != nil {
			t.Fatal(err)
		}
		recorded = updated
	}
	step := func(branch string, collapses bool) restack.Step {
		return restack.Step{Branch: branch, Parent: "synthetic-main", Collapses: collapses}
	}
	for _, test := range []struct {
		name  string
		steps []restack.Step
		want  string
	}{
		{name: "nothing", want: "Nothing needs replaying."},
		{name: "replays", steps: []restack.Step{step("synthetic-a", false)}, want: "Replays synthetic-a."},
		{name: "moves", steps: []restack.Step{step("synthetic-a", true), step("synthetic-b", true)}, want: "Moves synthetic-a and synthetic-b onto synthetic-main, where their work already is."},
		{name: "both", steps: []restack.Step{step("synthetic-a", true), step("synthetic-b", false)}, want: "Replays synthetic-b. Moves synthetic-a onto synthetic-main, where its work already is."},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := restack.Plan{Steps: test.steps}
			plan.Graph = recorded
			if got := replayNote(plan); got != test.want {
				t.Errorf("replayNote() = %q, want %q", got, test.want)
			}
		})
	}
}

// pull replays onto the ref it fetched the trunk into. That ref is g2g's own,
// and the preview named it as where a landed branch would move.
func TestPullPreviewNamesTheTrunkRatherThanTheFetchedRef(t *testing.T) {
	recorded, err := graph.New().Track("synthetic-a", graph.Edge{Parent: "synthetic-main", ForkPoint: "0000000000000000000000000000000000000000"})
	if err != nil {
		t.Fatal(err)
	}
	fetched := "refs/g2g/remotes/origin/synthetic-main"
	plan := restack.Plan{Onto: restack.ToLocation(fetched), Steps: []restack.Step{{Branch: "synthetic-a", Parent: fetched, Collapses: true}}}
	plan.Graph = recorded
	if got, want := replayNote(plan), "Moves synthetic-a onto synthetic-main, where its work already is."; got != want {
		t.Errorf("replayNote() = %q, want %q", got, want)
	}
}

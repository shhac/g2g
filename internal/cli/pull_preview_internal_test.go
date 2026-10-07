package cli

import (
	"testing"

	"github.com/shhac/g2g/internal/restack"
)

// A branch whose work is already in its new base is moved there rather than
// replayed, which still moves a ref. The preview said "Nothing needs
// replaying" over a stack whose every branch the apply then moved.
func TestPullPreviewNamesTheBranchesItMoves(t *testing.T) {
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
			if got := replayNote(restack.Plan{Steps: test.steps}); got != test.want {
				t.Errorf("replayNote() = %q, want %q", got, test.want)
			}
		})
	}
}

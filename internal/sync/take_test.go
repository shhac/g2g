package sync

import (
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/graph"
)

// The boundary is the branch and what it is stacked on, and everything above
// it keeps the default — which is to refuse rather than pick a side. That
// refusal is the point: a boundary says where you have decided, not that you
// have decided everywhere.
func TestATakeAppliesThroughItsBoundaryAndNoFurther(t *testing.T) {
	parents := map[string]string{"synthetic-a": "synthetic-main", "synthetic-b": "synthetic-a", "synthetic-c": "synthetic-b"}
	take, err := ParseTake("published", "synthetic-b")
	if err != nil {
		t.Fatal(err)
	}

	for branch, want := range map[string]bool{
		"synthetic-main": true,
		"synthetic-a":    true,
		"synthetic-b":    true,  // inclusive
		"synthetic-c":    false, // above it, so still refused
	} {
		if got := take.AppliesTo(branch, parents); got != want {
			t.Errorf("AppliesTo(%s) = %t, want %t", branch, got, want)
		}
	}
}

// Where the stack forks, a sibling is not below the boundary however the
// selection happens to be drawn. It was a position in a flattened list, so
// naming one fork discarded work on the other.
func TestATakeThroughOneForkDoesNotReachItsSibling(t *testing.T) {
	parents := map[string]string{"synthetic-a": "synthetic-main", "synthetic-b": "synthetic-a", "synthetic-c": "synthetic-a"}
	take, err := ParseTake("published", "synthetic-c")
	if err != nil {
		t.Fatal(err)
	}
	for branch, want := range map[string]bool{"synthetic-a": true, "synthetic-c": true, "synthetic-b": false} {
		if got := take.AppliesTo(branch, parents); got != want {
			t.Errorf("AppliesTo(%s) = %t, want %t", branch, got, want)
		}
	}
}

// A record naming a cycle ends the walk rather than the command.
func TestATakeStopsWalkingARecordedCycle(t *testing.T) {
	parents := map[string]string{"synthetic-a": "synthetic-b", "synthetic-b": "synthetic-a"}
	take, err := ParseTake("published", "synthetic-a")
	if err != nil {
		t.Fatal(err)
	}
	if take.AppliesTo("synthetic-elsewhere", parents) {
		t.Error("AppliesTo() reached a branch the cycle does not contain")
	}
}

func TestAnUnboundedTakeAppliesToEverySelectedBranch(t *testing.T) {
	parents := map[string]string{"synthetic-a": "synthetic-main", "synthetic-b": "synthetic-a"}
	take, err := ParseTake("published", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, branch := range []string{"synthetic-a", "synthetic-b"} {
		if !take.AppliesTo(branch, parents) {
			t.Errorf("AppliesTo(%s) = false, want the whole selection", branch)
		}
	}
}

// No side chosen means no branch is taken, boundary or not.
func TestNoSideTakesNothing(t *testing.T) {
	if TakeNothing.AppliesTo("synthetic-a", map[string]string{"synthetic-a": "synthetic-main"}) {
		t.Error("the default resolved a divergence")
	}
}

// The way out of a refusal keeps the selection it came from, and widens a
// boundary to cover what was refused when one branch covers it all.
func TestTheWayOutOfADivergenceKeepsTheSelection(t *testing.T) {
	parents := map[string]string{"synthetic-a": "synthetic-main", "synthetic-b": "synthetic-a", "synthetic-c": "synthetic-a"}
	bounded := Take{Side: SidePublished, Through: "synthetic-a"}
	for name, test := range map[string]struct {
		selection graph.Selection
		take      Take
		stuck     []divergence
		remote    string
		want      string
	}{
		"unbounded": {graph.Selection{}, TakeNothing, []divergence{{Branch: "synthetic-b"}}, "origin", "g2g pull --take published"},
		"selection": {graph.Selection{Branch: "synthetic-b", Scope: graph.ScopeTrunk}, TakeNothing, []divergence{{Branch: "synthetic-b"}}, "origin", "g2g pull --branch synthetic-b --scope trunk --take published"},
		"widened":   {graph.Selection{}, bounded, []divergence{{Branch: "synthetic-b"}}, "origin", "g2g pull --take published --through synthetic-b"},
		"forked":    {graph.Selection{}, bounded, []divergence{{Branch: "synthetic-b"}, {Branch: "synthetic-c"}}, "origin", "g2g pull --take published"},
		// Published is the version on the remote this pull read, so the way
		// out names that remote; without it the command takes origin's.
		"another remote": {graph.Selection{}, TakeNothing, []divergence{{Branch: "synthetic-b"}}, "upstream", "g2g pull --remote upstream --take published"},
	} {
		t.Run(name, func(t *testing.T) {
			ways := divergenceWays(test.selection, test.remote, test.take, parents, test.stuck)
			if got := ways[0].Command; got != test.want {
				t.Errorf("way out = %q, want %q", got, test.want)
			}
			if want := "take the version " + test.remote + " has"; !strings.Contains(ways[0].Effect, want) {
				t.Errorf("effect = %q, want it to say %q", ways[0].Effect, want)
			}
		})
	}
}

// A boundary on a decision nobody made resolves nothing, and would silently do
// the ordinary thing instead of saying so.
func TestABoundaryWithoutASideIsRefused(t *testing.T) {
	_, err := ParseTake("", "synthetic-b")
	if err == nil {
		t.Fatal("ParseTake() error = nil, want a refusal")
	}
	for _, want := range []string{"--through", "no side was chosen", "--take published"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestAnUnknownSideIsRefusedAndNamesWhatItTakes(t *testing.T) {
	_, err := ParseTake("theirs", "")
	if err == nil {
		t.Fatal("ParseTake() error = nil, want a refusal")
	}
	for _, want := range []string{"theirs", "published"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

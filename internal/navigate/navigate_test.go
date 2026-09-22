package navigate

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
)

// The forest every case walks:
//
//	synthetic-main
//	├─ synthetic-a
//	│  └─ synthetic-b
//	│     ├─ synthetic-c
//	│     └─ synthetic-d
//	│        └─ synthetic-e
//	└─ synthetic-other
//
// synthetic-main is a trunk of the g2g graph, so no source describes it as a
// branch in a stack — which is what resolution reports for a trunk.
var forest = shape.Forest{Parents: map[string]string{
	"synthetic-a":     "synthetic-main",
	"synthetic-b":     "synthetic-a",
	"synthetic-c":     "synthetic-b",
	"synthetic-d":     "synthetic-b",
	"synthetic-e":     "synthetic-d",
	"synthetic-other": "synthetic-main",
}}

// fakeSelector answers the way a source does: the stack through the branch,
// its trunk as the base, and its edges restricted to the selection.
type fakeSelector struct {
	forest shape.Forest
	// linear leaves Parents empty, as a source that predates forked selection
	// does, so the order has to carry the structure.
	linear bool
	absent []string
	asked  []stack.Selection
}

func (f *fakeSelector) Select(_ context.Context, selection stack.Selection, _ string) (stack.Snapshot, error) {
	f.asked = append(f.asked, selection)
	if _, tracked := f.forest.Parent(selection.Branch); !tracked {
		return stack.Snapshot{}, stack.Undescribed{Branch: selection.Branch, Trunk: selection.Branch == "synthetic-main"}
	}
	selected, err := f.forest.Select(selection.Branch, selection.Scope)
	if err != nil {
		return stack.Snapshot{}, err
	}
	snapshot := stack.Snapshot{Target: selection.Branch, Base: selected[0], Branches: selected[1:], Absent: f.absent, Source: stack.SourceG2G}
	if !f.linear {
		snapshot.Parents = f.forest.Restrict(selected)
	}
	return snapshot, nil
}

type fakeGit struct {
	current  string
	switched []string
}

func (g *fakeGit) CurrentBranch(context.Context) (string, error) {
	if g.current == "" {
		return "", errors.New("HEAD is detached")
	}
	return g.current, nil
}

func (g *fakeGit) SwitchExisting(_ context.Context, branch string) error {
	g.switched = append(g.switched, branch)
	return nil
}

// fakeRecorded is the g2g graph's own record of what sits on a branch.
type fakeRecorded struct{ forest shape.Forest }

func (r fakeRecorded) Children(_ context.Context, branch string) ([]string, error) {
	return r.forest.Children(branch), nil
}

func TestAMoveGoesExactlyWhereTheStructureLeadsAndRefusesToChoose(t *testing.T) {
	for _, test := range []struct {
		name  string
		from  string
		move  Direction
		steps int
		// to is the destination; blocked is a fragment of the refusal instead.
		to      string
		walked  []string
		blocked string
		// way is a command the refusal must offer.
		way string
	}{
		{name: "up one", from: "synthetic-a", move: Up, to: "synthetic-b"},
		{name: "up at a fork names the children", from: "synthetic-b", move: Up, blocked: "synthetic-b has 2 branches above it (synthetic-c, synthetic-d)", way: "git switch synthetic-d"},
		{name: "up several stops at the first fork", from: "synthetic-a", move: Up, steps: 3, blocked: "synthetic-b has 2 branches above it"},
		{name: "up from the top", from: "synthetic-e", move: Up, blocked: "top of its stack"},
		{name: "up past the top", from: "synthetic-d", move: Up, steps: 2, blocked: "synthetic-d is only 1 below the top", way: "g2g top"},
		{name: "down one", from: "synthetic-e", move: Down, to: "synthetic-d"},
		{name: "down several", from: "synthetic-e", move: Down, steps: 3, to: "synthetic-a", walked: []string{"synthetic-e", "synthetic-d", "synthetic-b", "synthetic-a"}},
		{name: "down from the bottom is the trunk", from: "synthetic-a", move: Down, to: "synthetic-main"},
		{name: "down past the trunk", from: "synthetic-b", move: Down, steps: 3, blocked: "synthetic-b is only 2 above the trunk", way: "g2g down 2"},
		{name: "down from the trunk", from: "synthetic-main", move: Down, blocked: "synthetic-main is the trunk"},
		{name: "top follows single children", from: "synthetic-d", move: Top, to: "synthetic-e"},
		{name: "top at a fork names the children", from: "synthetic-a", move: Top, blocked: "synthetic-b has 2 branches above it", way: "git switch synthetic-c"},
		{name: "top from the top has arrived", from: "synthetic-e", move: Top, to: "synthetic-e"},
		{name: "bottom from the middle", from: "synthetic-e", move: Bottom, to: "synthetic-a", walked: []string{"synthetic-e", "synthetic-d", "synthetic-b", "synthetic-a"}},
		{name: "bottom from the bottom has arrived", from: "synthetic-a", move: Bottom, to: "synthetic-a"},
		// Every stack on a trunk meets there, so off the trunk is a choice
		// whenever more than one stack is on it.
		{name: "up from a trunk with two stacks", from: "synthetic-main", move: Up, blocked: "synthetic-main has 2 branches above it (synthetic-a, synthetic-other)"},
		{name: "bottom from a trunk with two stacks", from: "synthetic-main", move: Bottom, blocked: "synthetic-main has 2 branches above it"},
	} {
		t.Run(test.name, func(t *testing.T) {
			git := &fakeGit{current: test.from}
			service := Service{Selector: &fakeSelector{forest: forest}, Git: git, Trunks: fakeRecorded{forest: forest}}

			move, err := service.Plan(context.Background(), Request{Direction: test.move, Steps: test.steps})
			if err != nil {
				t.Fatalf("Plan() error = %v", err)
			}

			if test.blocked != "" {
				if !strings.Contains(move.Blocked, test.blocked) {
					t.Fatalf("Blocked = %q, want it to contain %q", move.Blocked, test.blocked)
				}
				if move.Blocked != move.Repair.Sentence() {
					t.Errorf("Blocked %q is not the repair's sentence %q", move.Blocked, move.Repair.Sentence())
				}
				if test.way != "" && !slices.ContainsFunc(move.Repair.Ways, func(way repair.Step) bool { return way.Command == test.way }) {
					t.Errorf("repair offers %+v, want %q", move.Repair.Ways, test.way)
				}
				if err := service.Switch(context.Background(), move); err == nil || len(git.switched) != 0 {
					t.Errorf("a refused move switched to %v (err %v)", git.switched, err)
				}
				return
			}
			if move.Blocked != "" {
				t.Fatalf("Blocked = %q, want %s", move.Blocked, test.to)
			}
			if move.Destination != test.to {
				t.Errorf("Destination = %q, want %q", move.Destination, test.to)
			}
			if test.walked != nil && !slices.Equal(move.Walked, test.walked) {
				t.Errorf("Walked = %v, want %v", move.Walked, test.walked)
			}
			if err := service.Switch(context.Background(), move); err != nil {
				t.Fatal(err)
			}
			wantSwitched := []string{test.to}
			if test.to == test.from {
				wantSwitched = nil
			}
			if !slices.Equal(git.switched, wantSwitched) {
				t.Errorf("switched to %v, want %v", git.switched, wantSwitched)
			}
		})
	}
}

// A source that records no edges hands back a chain, and its order is its
// structure. Walking it must give the same answers as walking the edges.
func TestALinearSelectionWalksByItsOrder(t *testing.T) {
	for _, test := range []struct {
		from, to string
		move     Direction
	}{
		{from: "synthetic-e", to: "synthetic-d", move: Down},
		{from: "synthetic-e", to: "synthetic-a", move: Bottom},
		{from: "synthetic-d", to: "synthetic-e", move: Up},
	} {
		service := Service{Selector: &fakeSelector{forest: forest, linear: true}, Git: &fakeGit{current: test.from}}
		move, err := service.Plan(context.Background(), Request{Direction: test.move})
		if err != nil {
			t.Fatal(err)
		}
		if move.Destination != test.to || move.Blocked != "" {
			t.Errorf("%s from %s = %q (blocked %q), want %s", test.move, test.from, move.Destination, move.Blocked, test.to)
		}
	}
}

// Off a trunk with one stack on it, the rest of the walk is resolved from the
// branch it lands on, through the same selector as any other move.
func TestLeavingATrunkWithOneStackResolvesFromTheBranchAbove(t *testing.T) {
	single := shape.Forest{Parents: map[string]string{"synthetic-b": "synthetic-a", "synthetic-a": "synthetic-main"}}
	selector := &fakeSelector{forest: single}
	service := Service{Selector: selector, Git: &fakeGit{current: "synthetic-main"}, Trunks: fakeRecorded{forest: single}}

	move, err := service.Plan(context.Background(), Request{Direction: Top})
	if err != nil {
		t.Fatal(err)
	}

	if move.Destination != "synthetic-b" || move.Blocked != "" {
		t.Fatalf("top from the trunk = %q (blocked %q), want synthetic-b", move.Destination, move.Blocked)
	}
	if !slices.Equal(move.Walked, []string{"synthetic-main", "synthetic-a", "synthetic-b"}) {
		t.Errorf("Walked = %v", move.Walked)
	}
	if last := selector.asked[len(selector.asked)-1]; last.Branch != "synthetic-a" || last.Scope != shape.ScopeStack {
		t.Errorf("resolved from %+v, want the stack through synthetic-a", last)
	}
}

// A branch nothing describes and nothing is recorded on is refused with the
// resolver's own explanation, which already says what to run.
func TestAnUndescribedBranchIsRefusedWithTheResolversRemedy(t *testing.T) {
	service := Service{Selector: &fakeSelector{forest: forest}, Git: &fakeGit{current: "synthetic-loose"}, Trunks: fakeRecorded{forest: forest}}

	_, err := service.Plan(context.Background(), Request{Direction: Up})

	var undescribed stack.Undescribed
	if !errors.As(err, &undescribed) || undescribed.Branch != "synthetic-loose" {
		t.Fatalf("Plan() error = %v, want the resolver's Undescribed", err)
	}
}

// A destination the structure places but this machine does not have would be
// recreated from its remote-tracking ref by a guessing switch, so it is
// refused instead.
func TestADestinationThatIsNotLocalIsRefused(t *testing.T) {
	service := Service{Selector: &fakeSelector{forest: forest, absent: []string{"synthetic-d"}}, Git: &fakeGit{current: "synthetic-e"}}

	move, err := service.Plan(context.Background(), Request{Direction: Down})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(move.Blocked, "synthetic-d is not a local branch") {
		t.Errorf("Blocked = %q, want a refusal naming synthetic-d", move.Blocked)
	}
}

func TestADetachedHeadHasNowhereToMoveFrom(t *testing.T) {
	service := Service{Selector: &fakeSelector{forest: forest}, Git: &fakeGit{}}
	if _, err := service.Plan(context.Background(), Request{Direction: Up}); err == nil || !strings.Contains(err.Error(), "detached") {
		t.Errorf("Plan() error = %v, want a detached-HEAD refusal", err)
	}
}

// The selection a move asks for is the stack through the current branch, with
// the flags the user passed and nothing narrower.
func TestAMoveSelectsTheWholeStackThroughTheCurrentBranch(t *testing.T) {
	selector := &fakeSelector{forest: forest}
	service := Service{Selector: selector, Git: &fakeGit{current: "synthetic-e"}}
	if _, err := service.Plan(context.Background(), Request{Direction: Down, From: stack.SourceGraphite, Trunk: "synthetic-main"}); err != nil {
		t.Fatal(err)
	}
	want := stack.Selection{Branch: "synthetic-e", Trunk: "synthetic-main", Scope: shape.ScopeStack, From: stack.SourceGraphite}
	if len(selector.asked) != 1 || selector.asked[0] != want {
		t.Errorf("asked %+v, want %+v", selector.asked, want)
	}
}

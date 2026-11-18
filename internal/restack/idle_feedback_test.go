package restack

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/graph"
)

// Another worktree's restack is that worktree's to finish, so the way past it
// is to wait or finish it there; one whose worktree is gone has nobody to
// finish it, and only then is forgetting its journal offered. A worktree that
// cannot be found is not taken as gone: forgetting a live restack's journal
// loses what --abort puts back.
func TestAnotherWorktreesRestackNamesItsWayPastByWhereItIs(t *testing.T) {
	record := Record{Branch: "synthetic-busy"}
	for _, test := range []struct {
		name      string
		elsewhere Elsewhere
		want      []string
		rm        bool
	}{
		{"its checkout", Elsewhere{Record: record, Path: "/synthetic/journal.json", GitDir: "/synthetic/.git/worktrees/agent", Checkout: "/synthetic/agent"}, []string{`for branch "synthetic-busy"`, `in checkout "/synthetic/agent"`, "wait for it"}, false},
		{"its Git directory alone", Elsewhere{Record: record, Path: "/synthetic/journal.json", GitDir: "/synthetic/store.git"}, []string{"Git directory: /synthetic/store.git", "wait for it"}, false},
		{"nowhere it says", Elsewhere{Record: record, Path: "/synthetic/journal.json"}, []string{"in the worktree that started it", "wait for it"}, false},
		{"gone", Elsewhere{Record: record, Path: "/synthetic/journal.json", GitDir: "/synthetic/.git/worktrees/agent", Gone: true}, []string{"no longer exists", "rm /synthetic/journal.json"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			said := test.elsewhere.Repair([]string{"synthetic-busy"}).Sentence()
			for _, want := range test.want {
				if !strings.Contains(said, want) {
					t.Errorf("%q does not say %q", said, want)
				}
			}
			if strings.Contains(said, "rm ") != test.rm {
				t.Errorf("%q offers to forget the journal: %t, want %t", said, !test.rm, test.rm)
			}
		})
	}
}

// One restack stopped on a conflict in an agent's worktree refused every
// mutation in every worktree. Only the branches it is moving are untrue.
func TestApartRefusesOnlyTheBranchesARestackIsMoving(t *testing.T) {
	other := Elsewhere{Record: Record{
		Branch:    "synthetic-a",
		Original:  map[string]string{"synthetic-a": "1", "synthetic-b": "2"},
		Structure: map[string]RecordedEdge{"synthetic-a": {Parent: "main"}, "synthetic-b": {Parent: "synthetic-a"}},
	}, Checkout: "/synthetic/agent"}
	elsewhere := Service{Journal: &memoryJournal{others: []Elsewhere{other}}}
	for _, reads := range [][]string{{}, {"synthetic-other", "main"}} {
		if err := elsewhere.RequireApart(context.Background(), Reads(reads...)); err != nil {
			t.Errorf("reading %v beside another worktree's restack refused: %v", reads, err)
		}
	}
	for _, reads := range [][]string{{"synthetic-b"}, {"synthetic-child", "synthetic-a"}} {
		if err := elsewhere.RequireApart(context.Background(), Reads(reads...)); err == nil || !strings.Contains(err.Error(), "unfinished in checkout") {
			t.Errorf("reading %v while the restack moves it = %v, want the refusal", reads, err)
		}
	}
	if err := elsewhere.RequireIdle(context.Background()); err == nil {
		t.Error("a command that cannot say what it touches went ahead beside another worktree's restack")
	}

	here := Service{Journal: &memoryJournal{present: true, record: other.Record}}
	if err := here.RequireApart(context.Background(), Reads("synthetic-other")); err == nil || !strings.Contains(err.Error(), "already in progress") {
		t.Errorf("this worktree's own restack let another mutation go ahead: %v", err)
	}
	if err := here.RequireIdleHere(context.Background()); err == nil {
		t.Error("this worktree's own restack was not refused before discovery")
	}
}

// Two restacks overlap when either moves what the other reads. One paused
// part-way through replaying a child onto its parent is undone by another
// moving that parent, though neither moves what the other moves; two stacks
// on the same trunk overlap in neither direction, since a trunk never moves.
func TestRestacksOverlapWhenEitherMovesWhatTheOtherReads(t *testing.T) {
	paused := Record{
		Original:   map[string]string{"synthetic-child": "1"},
		Structure:  map[string]RecordedEdge{"synthetic-child": {Parent: "synthetic-parent"}},
		OntoParent: "synthetic-target",
	}.footprint()
	for _, test := range []struct {
		name string
		mine Footprint
		want []string
	}{
		{"moving the paused one's parent", footprintOf(set("synthetic-parent"), nil, nil, "").with("synthetic-parent", "main"), []string{"synthetic-parent"}},
		{"building on what it moves", footprintOf(set("synthetic-above"), nil, nil, "").with("synthetic-above", "synthetic-child"), []string{"synthetic-child"}},
		{"another stack on the same trunk", footprintOf(set("synthetic-free"), nil, nil, "").with("synthetic-free", "main"), nil},
		{"moving what it replays onto with --onto", footprintOf(set("synthetic-target"), nil, nil, ""), []string{"synthetic-target"}},
	} {
		if got := test.mine.meets(paused); !slices.Equal(got, test.want) {
			t.Errorf("%s: meets = %v, want %v", test.name, got, test.want)
		}
	}
}

// Two worktrees starting overlapping restacks at the same moment both pass
// the check before either has published its journal. Each looks again once
// its own is published, and steps back before anything moves.
func TestARestackThatFindsAnOverlapOnceItsJournalIsPublishedStepsBack(t *testing.T) {
	record := Record{Branch: "synthetic-a", Original: map[string]string{"synthetic-a": "1"}, Structure: map[string]RecordedEdge{"synthetic-a": {Parent: "main"}}}
	racing := &racingJournal{arrives: Elsewhere{Record: record, Checkout: "/synthetic/agent"}}
	err := (Service{Journal: racing}).publish(context.Background(), record)
	if err == nil || !strings.Contains(err.Error(), "which holds synthetic-a") {
		t.Fatalf("publish = %v, want it to step back over the restack that arrived", err)
	}
	if racing.present || racing.cleared != 1 {
		t.Errorf("the journal was left published: present %t, cleared %d", racing.present, racing.cleared)
	}
}

// racingJournal is a journal another worktree publishes an overlapping
// restack to the moment this one starts its own.
type racingJournal struct {
	memoryJournal
	arrives Elsewhere
}

func (j *racingJournal) Start(ctx context.Context, record Record) error {
	j.others = append(j.others, j.arrives)
	return j.memoryJournal.Start(ctx, record)
}

func set(branches ...string) map[string]bool {
	members := map[string]bool{}
	for _, branch := range branches {
		members[branch] = true
	}
	return members
}

// with is the footprint also reading branches.
func (f Footprint) with(branches ...string) Footprint {
	for _, branch := range branches {
		f.reads[branch] = true
	}
	return f
}

// A plan reads where it reparents and what its --onto names, as the record
// it journals does, so what admits a restack and what the others weigh it by
// are the same set.
func TestAPlanAndItsRecordReadTheOntoTargetAlike(t *testing.T) {
	plan := Plan{
		Discovery: graph.Discovery{
			Branches: []string{"synthetic-a"},
			Graph:    graph.Graph{Edges: map[string]graph.Edge{"synthetic-a": {Parent: "main"}}, Trunks: []string{"main"}},
		},
		Onto: ToBranch("synthetic-target"),
	}
	record := Record{Original: map[string]string{"synthetic-a": "1"}, Structure: map[string]RecordedEdge{"synthetic-a": {Parent: "main"}}, OntoParent: "synthetic-target"}
	mover := footprintOf(set("synthetic-target"), nil, nil, "")
	for name, footprint := range map[string]Footprint{"plan": plan.Footprint(), "record": record.footprint()} {
		if got := mover.meets(footprint); !slices.Equal(got, []string{"synthetic-target"}) {
			t.Errorf("%s: meets = %v, want the --onto target", name, got)
		}
	}
}

// A pull fast-forwards the trunk every stack replays onto. Where the trunk was
// stays inside it, so a restack stopped while replaying onto it loses nothing;
// counting the advance as a move made every pull wait for every stack's
// conflict. It still meets a branch the restack is rewriting, and a trunk
// reset to a rewritten one is a move like any other.
func TestAnAdvanceMeetsWhatAnotherMovesAndNotWhatItReads(t *testing.T) {
	paused := Record{
		Original:  map[string]string{"synthetic-child": "1"},
		Structure: map[string]RecordedEdge{"synthetic-child": {Parent: "main"}},
	}.footprint()
	for _, test := range []struct {
		name string
		mine Footprint
		want []string
	}{
		{"advancing the trunk it replays onto", Advances("main").With(Moves("synthetic-free")), nil},
		{"resetting the trunk it replays onto", Moves("main").With(Moves("synthetic-free")), []string{"main"}},
		{"advancing a branch it is rewriting", Advances("synthetic-child"), []string{"synthetic-child"}},
		{"forgetting a branch it is rewriting", Moves("synthetic-child"), []string{"synthetic-child"}},
		{"reading the trunk beside it", Reads("main", "").With(Moves("")), nil},
	} {
		if got := test.mine.meets(paused); !slices.Equal(got, test.want) {
			t.Errorf("%s: meets = %v, want %v", test.name, got, test.want)
		}
		if got := paused.meets(test.mine); !slices.Equal(got, test.want) {
			t.Errorf("%s, asked the other way round: meets = %v, want %v", test.name, got, test.want)
		}
	}
}

// A command that waits for every restack says that is why. Naming only the
// other worktree's restack read as that restack holding this command's
// branches, beside commands on the same branches that went ahead.
func TestACommandThatWaitsForEveryRestackSaysWhy(t *testing.T) {
	other := Elsewhere{Record: Record{Branch: "synthetic-a"}, Checkout: "/synthetic/agent"}
	err := Service{Journal: &memoryJournal{others: []Elsewhere{other}}}.RequireIdle(context.Background())
	for _, want := range []string{`for branch "synthetic-a" is unfinished in checkout "/synthetic/agent"`, "cannot name the branches it would touch", "wait for it"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("RequireIdle = %v, want it to say %q", err, want)
		}
	}
}

package restack

import (
	"context"
	"slices"
	"strings"
	"testing"
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
		if err := elsewhere.RequireApart(context.Background(), Footprint{Reads: reads}); err != nil {
			t.Errorf("reading %v beside another worktree's restack refused: %v", reads, err)
		}
	}
	for _, reads := range [][]string{{"synthetic-b"}, {"synthetic-child", "synthetic-a"}} {
		if err := elsewhere.RequireApart(context.Background(), Footprint{Reads: reads}); err == nil || !strings.Contains(err.Error(), "unfinished in checkout") {
			t.Errorf("reading %v while the restack moves it = %v, want the refusal", reads, err)
		}
	}
	if err := elsewhere.RequireIdle(context.Background()); err == nil {
		t.Error("a command that cannot say what it touches went ahead beside another worktree's restack")
	}

	here := Service{Journal: &memoryJournal{present: true, record: other.Record}}
	if err := here.RequireApart(context.Background(), Footprint{Reads: []string{"synthetic-other"}}); err == nil || !strings.Contains(err.Error(), "already in progress") {
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
		Original:  map[string]string{"synthetic-child": "1"},
		Structure: map[string]RecordedEdge{"synthetic-child": {Parent: "synthetic-parent"}},
	}.footprint()
	for _, test := range []struct {
		name string
		mine Footprint
		want []string
	}{
		{"moving the paused one's parent", Footprint{Moves: []string{"synthetic-parent"}, Reads: []string{"synthetic-parent", "main"}}, []string{"synthetic-parent"}},
		{"building on what it moves", Footprint{Moves: []string{"synthetic-above"}, Reads: []string{"synthetic-above", "synthetic-child"}}, []string{"synthetic-child"}},
		{"another stack on the same trunk", Footprint{Moves: []string{"synthetic-free"}, Reads: []string{"synthetic-free", "main"}}, nil},
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

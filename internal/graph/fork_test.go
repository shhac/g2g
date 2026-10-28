package graph

import (
	"context"
	"errors"
	"testing"
)

type forkOrdering map[[2]string]bool

func (o forkOrdering) IsAncestor(_ context.Context, ancestor, descendant string) (bool, error) {
	if ancestor == "synthetic-broken" {
		return false, errors.New("synthetic ancestry failure")
	}
	return o[[2]string{ancestor, descendant}], nil
}

type forkBases map[[2]string]string

func (b forkBases) MergeBase(_ context.Context, one, other string) (string, error) {
	shared, ok := b[[2]string{one, other}]
	if !ok {
		return "", errors.New("synthetic: no merge base")
	}
	return shared, nil
}

// A boundary only ever moves forward, to where the trunk and the branch meet:
// the commits below that are the trunk's. A meeting point the boundary is not
// below leaves it where it was, and a failure to ask says so.
func TestLaterForkOnlyMovesABoundaryForward(t *testing.T) {
	bases := forkBases{{"synthetic-trunk", "synthetic-head"}: "synthetic-shared"}
	ordering := forkOrdering{{"synthetic-old", "synthetic-shared"}: true}
	for _, test := range []struct {
		name, tip, fork, want string
		fails                 bool
	}{
		{name: "the trunk moved past it", tip: "synthetic-trunk", fork: "synthetic-old", want: "synthetic-shared"},
		{name: "already past the meeting point", tip: "synthetic-trunk", fork: "synthetic-newer", want: "synthetic-newer"},
		{name: "no meeting point", tip: "synthetic-elsewhere", fork: "synthetic-old", fails: true},
		{name: "ancestry cannot be read", tip: "synthetic-trunk", fork: "synthetic-broken", want: "synthetic-broken", fails: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := LaterFork(context.Background(), ordering, bases, test.tip, "synthetic-head", test.fork)
			if (err != nil) != test.fails {
				t.Fatalf("error = %v, want failure %t", err, test.fails)
			}
			if got != test.want {
				t.Errorf("LaterFork = %q, want %q", got, test.want)
			}
		})
	}
}

// upstreamAncestry is fakeAncestry with a known upstream for every trunk,
// which is the capability AheadOnUpstream asks for.
type upstreamAncestry struct {
	fakeAncestry
	known string
	err   error
	asked *int
}

func (f upstreamAncestry) KnownParent(context.Context, string) (string, error) {
	*f.asked++
	return f.known, f.err
}

// Every way of answering no matters as much as the yes: a branch read as on a
// lagging trunk gets no step, so a wrong yes is a stale branch nothing repairs.
func TestAheadOnUpstreamAnswersOnlyForAForkOnTheTrunksUpstream(t *testing.T) {
	recorded := Graph{
		Edges:  map[string]Edge{"synthetic-feature": {Parent: "synthetic-trunk", ForkPoint: "synthetic-fork"}},
		Trunks: []string{"synthetic-trunk"},
	}
	// synthetic-tip lags synthetic-fork, which synthetic-upstream contains;
	// synthetic-elsewhere is a fork past the tip that the upstream never had.
	ancestors := map[string][]string{
		"synthetic-fork":      {"synthetic-tip"},
		"synthetic-elsewhere": {"synthetic-tip"},
		"synthetic-upstream":  {"synthetic-tip", "synthetic-fork"},
	}
	for _, test := range []struct {
		name         string
		parent, fork string
		known        string
		err          error
		plain        bool
		want         bool
		wantErr      bool
		asks         int
	}{
		{name: "a fork on the upstream", parent: "synthetic-trunk", fork: "synthetic-fork", known: "synthetic-upstream", want: true, asks: 1},
		{name: "a fork the upstream never had", parent: "synthetic-trunk", fork: "synthetic-elsewhere", known: "synthetic-upstream", asks: 1},
		{name: "no known upstream", parent: "synthetic-trunk", fork: "synthetic-fork", asks: 1},
		{name: "a trunk that moved on, asked nothing more", parent: "synthetic-trunk", fork: "synthetic-older", known: "synthetic-upstream"},
		{name: "a fork at the tip", parent: "synthetic-trunk", fork: "synthetic-tip", known: "synthetic-upstream"},
		{name: "a parent that is not a trunk", parent: "synthetic-feature", fork: "synthetic-fork", known: "synthetic-upstream"},
		{name: "a reader that cannot say", parent: "synthetic-trunk", fork: "synthetic-fork", plain: true},
		{name: "a failed read", parent: "synthetic-trunk", fork: "synthetic-fork", err: errors.New("synthetic failure"), wantErr: true, asks: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			asked := 0
			var git ordering = upstreamAncestry{fakeAncestry: fakeAncestry{ancestors: ancestors}, known: test.known, err: test.err, asked: &asked}
			if test.plain {
				git = fakeAncestry{ancestors: ancestors}
			}
			got, err := AheadOnUpstream(context.Background(), git, recorded, test.parent, "synthetic-tip", test.fork)
			if got != test.want || (err != nil) != test.wantErr || asked != test.asks {
				t.Errorf("AheadOnUpstream() = %t, %v after %d upstream reads; want %t, error %t, %d reads", got, err, asked, test.want, test.wantErr, test.asks)
			}
		})
	}
}

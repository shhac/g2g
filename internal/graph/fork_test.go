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

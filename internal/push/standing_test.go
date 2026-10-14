package push

import (
	"testing"

	"github.com/shhac/g2g/internal/syncpoint"
)

// What a sync point's changes say of publishing a branch, arm by arm. The
// replay arms are the ones a fake Git cannot reach truthfully, and a remote
// replay this clone has not taken is the case push must refuse: publishing
// over it would erase a colleague's resolved conflict.
func TestStandingFromWhatTheSyncPointSaid(t *testing.T) {
	pair := []syncpoint.Replacement{{Old: "old", New: "new"}}
	for _, test := range []struct {
		name    string
		changes syncpoint.Changes
		want    Publication
		decided bool
	}{
		{
			name:    "a drop upstream would be put back",
			changes: syncpoint.Changes{DroppedUpstream: []string{"x"}, Mine: []string{"m"}, New: []string{"n"}, Shared: []string{"s"}},
			want:    Publication{Standing: Restoring, Ours: 2, Theirs: 1, Dropped: 1},
			decided: true,
		},
		{
			name:    "a colleague's replay this clone has not taken",
			changes: syncpoint.Changes{DroppedUpstream: []string{"old"}, New: []string{"new"}, ReplacedUpstream: pair, Shared: []string{"s"}},
			want:    Publication{Standing: Diverged, Theirs: 1},
			decided: true,
		},
		{
			name:    "somebody's commit on top of a drop here",
			changes: syncpoint.Changes{DroppedHere: []string{"x"}, New: []string{"n"}, Shared: []string{"s"}},
			want:    Publication{Standing: Diverged, Theirs: 2},
			decided: true,
		},
		{
			name:    "every commit of the branch's own gone",
			changes: syncpoint.Changes{DroppedHere: []string{"x"}},
			decided: false,
		},
		{
			name:    "a drop here alongside a replay of another commit",
			changes: syncpoint.Changes{DroppedHere: []string{"x", "old"}, ReplacedHere: pair, Mine: []string{"m", "new"}},
			want:    Publication{Standing: Dropping, Ours: 2, Dropped: 1, Replaced: 1},
			decided: true,
		},
		{
			name:    "only replays of agreed commits",
			changes: syncpoint.Changes{DroppedHere: []string{"old"}, Mine: []string{"new"}, ReplacedHere: pair},
			want:    Publication{Standing: Rewritten, Ours: 1, Replaced: 1},
			decided: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, decided := standing(test.changes)
			if decided != test.decided || got != test.want {
				t.Errorf("standing() = %+v, %t; want %+v, %t", got, decided, test.want, test.decided)
			}
		})
	}
}

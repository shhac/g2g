package push

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/syncpoint"
	"github.com/shhac/g2g/internal/testutil/replays"
)

// What push makes of every shape a sync point's answer can take: whether the
// answer decides the branch at all, the standing it gives, the commits a
// preview names, and the refusal a commit the remote dropped meets. The golden
// file is the whole table: a change to how the answer is held that changed
// anything push does would show here as a diff.
func TestPushStandsEveryShapeOfASyncPointsAnswer(t *testing.T) {
	var out strings.Builder
	for _, shape := range replays.All() {
		changes := shape.Changes()
		fmt.Fprintf(&out, "%s: decides=%t", shape, changes.Dropped())
		if publication, decided := standing(changes); decided {
			fmt.Fprintf(&out, " %+v", publication)
		} else {
			out.WriteString(" undecided")
		}
		drops, err := Service{Git: &fakeGit{}}.drops(context.Background(), []string{"synthetic-b"}, map[string]syncpoint.Changes{"synthetic-b": changes})
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&out, " naming %s", replays.Drops(drops))
		if restores := syncpoint.Of(drops, syncpoint.Restoring); len(restores) != 0 {
			fmt.Fprintf(&out, " refuses %s", restoring("origin", restores).Sentence())
		}
		out.WriteString("\n")
	}
	replays.Golden(t, "replays.golden", out.String())
}

package sync

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/testutil/replays"
)

// What pull makes of every shape a sync point's answer can take, with each
// commit it could be asked to keep, the commit the remote dropped moved to
// another branch or not, and a reset to a stale tracking ref or not. The
// golden file is the whole decision table: a change to how the answer is held
// that changed anything pull does would show here as a diff.
func TestPullDecidesEveryShapeOfASyncPointsAnswer(t *testing.T) {
	var out strings.Builder
	for _, shape := range replays.All() {
		keeps := []string{""}
		for _, present := range []struct {
			on     bool
			commit string
		}{
			{shape.DroppedHere, replays.DroppedHere}, {shape.DroppedUpstream, replays.DroppedUpstream},
			{shape.ReplacedHere, replays.ReplacedHere}, {shape.ReplacedUpstream, replays.ReplacedUpstream},
		} {
			if present.on {
				keeps = append(keeps, present.commit)
			}
		}
		moves := []bool{false}
		if shape.DroppedUpstream {
			moves = append(moves, true)
		}
		// A reset to a stale tracking ref reads every commit it left out as
		// new, and leaves nothing dropped here.
		stales := []bool{false}
		if shape.New && !shape.DroppedHere && !shape.ReplacedHere {
			stales = append(stales, true)
		}
		for _, keep := range keeps {
			for _, moved := range moves {
				for _, stale := range stales {
					fmt.Fprintf(&out, "%s keep=%s moved=%t stale=%t: %s\n", shape, keep, moved, stale, judged(t, shape, keep, moved, stale))
				}
			}
		}
	}
	replays.Golden(t, "replays.golden", out.String())
}

func judged(t *testing.T, shape replays.Case, keep string, moved, stale bool) string {
	t.Helper()
	changes := shape.Changes()
	changes.Stale = stale
	c := collecting{
		remote: "origin", command: "g2g pull", takeCommand: "g2g pull --take published",
		branches: []string{"synthetic-b", "synthetic-c"}, onRemote: map[string]string{"synthetic-c": "synthetic-c-tip"},
	}
	if keep != "" {
		c.keep = []string{keep}
	}
	git := &fakeGit{ancestors: map[string][]string{}}
	if moved {
		git.ancestors["synthetic-c-tip"] = []string{replays.DroppedUpstream}
	}
	got, decided, err := Service{Git: git}.judge(context.Background(), c, "synthetic-b", "published-tip", "", changes)
	if err != nil {
		t.Fatal(err)
	}
	said := fmt.Sprintf("decided=%t", decided)
	if got.refusal != nil {
		said += " refuses " + got.refusal.Sentence()
	}
	if got.collection != nil {
		said += fmt.Sprintf(" takes %s superseded=%t dropping %v", got.collection.To, got.collection.Superseded, got.collection.Dropped)
	}
	return said + " naming " + replays.Drops(got.commits)
}

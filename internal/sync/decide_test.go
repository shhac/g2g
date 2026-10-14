package sync

import (
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/syncpoint"
)

// What pull does with a branch something was dropped from, decided from what
// the sync point said alone. Every refusal names its way out, and the one row
// that takes the published version takes it with exactly the commits that
// left the stack named as dropped.
func TestDecideTakesOnlyAClearDropAndRefusesEveryMix(t *testing.T) {
	c := collecting{remote: "origin", command: "g2g pull", takeCommand: "g2g pull --take published"}
	moved := []Drop{{Kind: syncpoint.Moved, Branch: "synthetic-b", Commit: "m0000000000000", To: "synthetic-c"}}
	for _, test := range []struct {
		name    string
		changes syncpoint.Changes
		gone    []string
		refuses string
		// unwant is what the refusal must not offer: a --keep about
		// replacements alone names nothing a keep can usefully take.
		unwant   string
		takes    bool
		replaced int
		left     int
	}{
		{name: "dropped upstream alone is taken", changes: syncpoint.Changes{DroppedUpstream: []string{"x0000000000000"}, Shared: []string{"b"}}, gone: []string{"x0000000000000"}, takes: true},
		{name: "dropped here alone is left for push", changes: syncpoint.Changes{DroppedHere: []string{"x0000000000000"}}, left: 1},
		{name: "dropped here with new there refuses", changes: syncpoint.Changes{DroppedHere: []string{"x0000000000000"}, New: []string{"z"}}, refuses: "--keep x00000000000"},
		{name: "dropped both ways refuses", changes: syncpoint.Changes{DroppedUpstream: []string{"x0000000000000"}, DroppedHere: []string{"y0000000000000"}}, refuses: "on both sides"},
		{name: "yours over their drop refuses", changes: syncpoint.Changes{DroppedUpstream: []string{"x0000000000000"}, Mine: []string{"m"}, Shared: []string{"b"}}, gone: []string{"x0000000000000"}, refuses: "--take published"},
		{name: "an emptied branch refuses", changes: syncpoint.Changes{DroppedUpstream: []string{"x0000000000000"}}, gone: []string{"x0000000000000"}, refuses: "emptied"},
		{name: "a move alone empties nothing", changes: syncpoint.Changes{DroppedUpstream: []string{"m0000000000000"}}, takes: true},
		{name: "replayed here with new there refuses", changes: syncpoint.Changes{DroppedHere: []string{"r0000000000000"}, Mine: []string{"n0000000000000"}, ReplacedHere: []syncpoint.Replacement{{Old: "r0000000000000", New: "n0000000000000"}}, New: []string{"z"}}, refuses: "you dropped or replayed r00000000000", unwant: "--keep"},
		{name: "replayed here and dropped there refuses", changes: syncpoint.Changes{DroppedHere: []string{"r0000000000000"}, Mine: []string{"n0000000000000"}, ReplacedHere: []syncpoint.Replacement{{Old: "r0000000000000", New: "n0000000000000"}}, DroppedUpstream: []string{"x0000000000000"}, Shared: []string{"b"}}, gone: []string{"x0000000000000"}, refuses: "on both sides"},
		{name: "yours over their replay refuses", changes: syncpoint.Changes{DroppedUpstream: []string{"r0000000000000"}, New: []string{"n0000000000000"}, ReplacedUpstream: []syncpoint.Replacement{{Old: "r0000000000000", New: "n0000000000000"}}, Mine: []string{"m"}, Shared: []string{"b"}}, refuses: "--take published", unwant: "--keep"},
		{name: "their replay alone is taken, naming what it replaced", changes: syncpoint.Changes{DroppedUpstream: []string{"r0000000000000"}, New: []string{"n0000000000000"}, ReplacedUpstream: []syncpoint.Replacement{{Old: "r0000000000000", New: "n0000000000000"}}}, takes: true, replaced: 1},
		{name: "a branch wholly replayed there is not emptied", changes: syncpoint.Changes{DroppedUpstream: []string{"x0000000000000", "r0000000000000"}, New: []string{"n0000000000000"}, ReplacedUpstream: []syncpoint.Replacement{{Old: "r0000000000000", New: "n0000000000000"}}}, gone: []string{"x0000000000000"}, takes: true, replaced: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := decide(c, "synthetic-b", "published-tip", "", test.changes, moved, test.gone, verdict{})
			switch {
			case test.refuses != "":
				if got.refusal == nil || !strings.Contains(got.refusal.Sentence(), test.refuses) || got.collection != nil {
					t.Errorf("decide() = %+v, want a refusal naming %q", got, test.refuses)
				}
				if test.unwant != "" && got.refusal != nil && strings.Contains(got.refusal.Sentence(), test.unwant) {
					t.Errorf("refusal %q offers %q", got.refusal.Sentence(), test.unwant)
				}
			case test.takes:
				if got.collection == nil || got.collection.To != "published-tip" || len(syncpoint.Of(got.commits, syncpoint.Dropped)) != len(test.gone) || got.refusal != nil {
					t.Errorf("decide() = %+v, want the published version taken, dropping %v", got, test.gone)
				}
				if replaced := syncpoint.Of(got.commits, syncpoint.Replaced); len(replaced) != test.replaced || (test.replaced != 0 && replaced[0].To != "n0000000000000") {
					t.Errorf("replaced = %+v, want %d naming its replay", replaced, test.replaced)
				}
			default:
				if len(syncpoint.Of(got.commits, syncpoint.Left)) != test.left || got.collection != nil || got.refusal != nil {
					t.Errorf("decide() = %+v, want %d left for push", got, test.left)
				}
			}
		})
	}
}

// Package replays enumerates what a sync point can say about one branch, for
// tests that pin what push and pull make of each answer. It is apart from
// testutil because it names syncpoint's types, and syncpoint's own tests use
// testutil.
package replays

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/syncpoint"
)

// Case is one shape of a sync point's answer: which of the drops, the new
// commits on either side, and the replays it pairs are there. push and pull
// each pin what they make of every shape against a golden file, so a change
// to how the answer is held shows up as a diff in what either command does,
// rather than only in a test written for the change.
type Case struct {
	DroppedHere, DroppedUpstream, Mine, New, ReplacedHere, ReplacedUpstream, Shared bool
}

// The commits a Case names. Each is longer than a short id, so anything a
// command shortens reads as shortened.
const (
	DroppedHere      = "drophere-0000-a"
	DroppedUpstream  = "dropthere-000-a"
	Mine             = "mine-00000000-a"
	New              = "new-000000000-a"
	ReplacedHere     = "replacedhere-0a"
	ReplayHere       = "replayhere-000a"
	ReplacedUpstream = "replacedthere-a"
	ReplayUpstream   = "replaythere-00a"
	Shared           = "shared-000000-a"
)

// All is every Case there is.
func All() []Case {
	all := make([]Case, 0, 1<<7)
	for bits := range 1 << 7 {
		on := func(bit int) bool { return bits&(1<<bit) != 0 }
		all = append(all, Case{
			DroppedHere: on(0), DroppedUpstream: on(1), Mine: on(2), New: on(3),
			ReplacedHere: on(4), ReplacedUpstream: on(5), Shared: on(6),
		})
	}
	return all
}

// Changes is the Case as a sync point's assessment holds it.
func (c Case) Changes() syncpoint.Changes {
	changes := syncpoint.Changes{
		Mine: make([]string, 0), New: make([]string, 0),
		DroppedHere: make([]string, 0), DroppedUpstream: make([]string, 0),
	}
	if c.DroppedHere {
		changes.DroppedHere = append(changes.DroppedHere, DroppedHere)
	}
	if c.DroppedUpstream {
		changes.DroppedUpstream = append(changes.DroppedUpstream, DroppedUpstream)
	}
	if c.Mine {
		changes.Mine = append(changes.Mine, Mine)
	}
	if c.New {
		changes.New = append(changes.New, New)
	}
	if c.ReplacedHere {
		changes.ReplacedHere = syncpoint.Replacements{{Old: ReplacedHere, New: ReplayHere}}
	}
	if c.ReplacedUpstream {
		changes.ReplacedUpstream = syncpoint.Replacements{{Old: ReplacedUpstream, New: ReplayUpstream}}
	}
	if c.Shared {
		changes.Shared = []string{Shared}
	}
	return changes
}

// String names the parts present, which is what a golden line opens with.
func (c Case) String() string {
	parts := make([]string, 0, 7)
	for _, part := range []struct {
		on   bool
		name string
	}{
		{c.DroppedHere, "dropped-here"}, {c.DroppedUpstream, "dropped-upstream"}, {c.Mine, "mine"}, {c.New, "new"},
		{c.ReplacedHere, "replaced-here"}, {c.ReplacedUpstream, "replaced-upstream"}, {c.Shared, "shared"},
	} {
		if part.on {
			parts = append(parts, part.name)
		}
	}
	if len(parts) == 0 {
		return "nothing"
	}
	return strings.Join(parts, "+")
}

var update = flag.Bool("update-replays", false, "rewrite the replay golden files")

// Golden pins got to testdata/name. Regenerate with -update-replays.
func Golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (regenerate with -update-replays)", path, err)
	}
	if got != string(want) {
		t.Errorf("output does not match %s (regenerate with -update-replays and review the diff)", path)
	}
}

// Drops writes the commits a command names, in its order, for a golden line.
func Drops(drops []syncpoint.Drop) string {
	written := make([]string, 0, len(drops))
	for _, drop := range drops {
		line := string(drop.Kind) + " " + drop.Branch + ":" + drop.Commit
		if drop.To != "" {
			line += "->" + drop.To
		}
		written = append(written, line)
	}
	return "[" + strings.Join(written, ", ") + "]"
}

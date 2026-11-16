package cli_test

import (
	"fmt"
	"strings"
	"testing"

	localgit "github.com/shhac/g2g/internal/git"
)

// Reparenting changes where a branch will land, not which commits it owns.
// The sibling starts on an older trunk, so its merge base cannot delimit the
// recorded branch's work.
func TestJourneyTrackReparentKeepsOwnRangeOnOlderSibling(t *testing.T) {
	w := newWorld(t)
	w.git(w.Local, "branch", "-m", "main", "synthetic-trunk")
	w.branchOff("synthetic-trunk", "synthetic-a", "synthetic-a.txt")
	mustRun(t, "track", "--parent", "synthetic-trunk", "--apply")
	w.assertClean(w.Local)
	for n := 2; n <= 5; n++ {
		name := fmt.Sprintf("synthetic-trunk-%d.txt", n)
		w.commit(w.Local, "synthetic-trunk", name, name)
	}
	fork := w.tip(w.Local, "synthetic-trunk")
	w.branchOff("synthetic-trunk", "synthetic-b", "synthetic-b.txt")
	mustRun(t, "track", "--parent", "synthetic-trunk", "--apply")
	w.assertClean(w.Local)

	before, tip := w.readStore(), w.tip(w.Local, "synthetic-b")
	preview := mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a")
	if !strings.Contains(preview, localgit.Short(fork)+"..synthetic-b") {
		t.Errorf("track preview omits the preserved replay range:\n%s", preview)
	}
	if w.readStore() != before || w.tip(w.Local, "synthetic-b") != tip {
		t.Fatal("track preview changed the record or branch")
	}
	w.assertClean(w.Local)
	mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a", "--apply")
	w.assertClean(w.Local)
	if got := w.tip(w.Local, "refs/g2g/forkpoints/synthetic-b"); got != fork {
		t.Errorf("reparent widened the replay range: fork = %s, want recorded trunk fork %s", got, fork)
	}
	preview = mustRun(t, "restack", "--branch", "synthetic-b")
	if !strings.Contains(preview, "Replay range for synthetic-b: "+localgit.Short(fork)+".."+localgit.Short(tip)) {
		t.Errorf("restack preview omits the actual replay range:\n%s", preview)
	}
	w.assertClean(w.Local)
	mustRun(t, "restack", "--branch", "synthetic-b", "--apply")
	w.assertClean(w.Local)
	if got := w.git(w.Local, "log", "--format=%s", "synthetic-a..synthetic-b"); got != "synthetic synthetic-b.txt" {
		t.Errorf("replayed trunk commits with the branch's own commit:\n%s", got)
	}
	if got := w.git(w.Local, "diff", "--name-only", "synthetic-a", "synthetic-b"); strings.TrimSpace(got) != "synthetic-b.txt" {
		t.Errorf("branch contains changes outside its own range:\n%s", got)
	}
}

func TestJourneyTrackReparentRefusesAnInvalidRecordedRange(t *testing.T) {
	w := newWorld(t)
	w.git(w.Local, "branch", "-m", "main", "synthetic-trunk")
	w.branchOff("synthetic-trunk", "synthetic-a", "synthetic-a.txt")
	mustRun(t, "track", "--parent", "synthetic-trunk", "--apply")
	w.assertClean(w.Local)
	w.commit(w.Local, "synthetic-trunk", "synthetic-trunk.txt", "synthetic trunk advanced")
	w.branchOff("synthetic-trunk", "synthetic-b", "synthetic-b.txt")
	mustRun(t, "track", "--parent", "synthetic-trunk", "--apply")
	w.assertClean(w.Local)
	// A damaged record claims a sibling's tip, which is not in the branch.
	fork := w.tip(w.Local, "refs/g2g/forkpoints/synthetic-b")
	w.rewriteStore(func(stored string) string {
		return strings.ReplaceAll(stored, fork, w.tip(w.Local, "synthetic-a"))
	})
	before, tip := w.readStore(), w.tip(w.Local, "synthetic-b")
	preview := mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a")
	for _, want := range []string{"own replay range cannot be determined", "g2g track --branch synthetic-b"} {
		if !strings.Contains(preview, want) {
			t.Errorf("preview omits %q:\n%s", want, preview)
		}
	}
	if _, _, err := run(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a", "--apply"); err == nil {
		t.Fatal("reparenting accepted an invalid recorded range")
	}
	if w.readStore() != before || w.tip(w.Local, "synthetic-b") != tip || w.tip(w.Local, "refs/g2g/forkpoints/synthetic-b") != fork {
		t.Fatal("refusal changed the record, branch or fork-point pin")
	}
	w.assertClean(w.Local)
	// The named repair works: recording its actual parent establishes the
	// boundary again, after which reparenting preserves that verified range.
	mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-trunk", "--apply")
	w.assertClean(w.Local)
	mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a", "--apply")
	w.assertClean(w.Local)
	if got := w.tip(w.Local, "refs/g2g/forkpoints/synthetic-b"); got != fork {
		t.Errorf("repaired reparent fork = %s, want %s", got, fork)
	}
}

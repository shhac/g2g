package syncpoint

import (
	"context"
	"os"
	"slices"
	"testing"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
)

// These are questions about which commits Git considers the same, which only
// real Git can answer. Each side of a branch is a branch in one throwaway
// repository: "synthetic-b" is this clone's, "synthetic-published" the
// remote's, and "synthetic-synced" where the two last agreed.

type fixture struct {
	repo   testutil.GitRepo
	client localgit.Client
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	repo := testutil.NewGitRepo(t, "synthetic-main")
	repo.Commit("synthetic base", "base.txt", "base")
	repo.Run("switch", "-qc", "synthetic-a")
	repo.Commit("synthetic a", "a.txt", "a")
	repo.Run("switch", "-qc", "synthetic-b")
	repo.Commit("synthetic b one", "b1.txt", "b1")
	repo.Commit("synthetic x", "x.txt", "x")
	t.Chdir(repo.Dir)
	return fixture{repo: repo, client: localgit.Client{Runner: subprocess.ExecRunner{}}}
}

func (f fixture) classify(t *testing.T, local, remote, synced string) Changes {
	t.Helper()
	branch := Branch{
		Local: f.repo.Revision(local), Remote: f.repo.Revision(remote),
		LocalParent: f.repo.Revision("synthetic-a"),
	}
	if synced != "" {
		branch.Sync, branch.Synced = localgit.SyncPoint{Tip: f.repo.Revision(synced)}, true
	}
	changes, err := Classify(context.Background(), f.client, branch)
	if err != nil {
		t.Fatalf("Classify() error = %v", err)
	}
	return changes
}

func (f fixture) ids(revisions ...string) []string {
	ids := make([]string, 0, len(revisions))
	for _, revision := range revisions {
		ids = append(ids, f.repo.Revision(revision))
	}
	return ids
}

// Without a sync point every difference is new work on its own side, which is
// what pull and push assumed before there were any.
func TestWithoutASyncPointEveryDifferenceIsNew(t *testing.T) {
	f := newFixture(t)
	f.repo.Run("branch", "synthetic-published", "synthetic-b~1")
	got := f.classify(t, "synthetic-b", "synthetic-published", "")
	if !slices.Equal(got.Mine, f.ids("synthetic-b")) || len(got.New)+len(got.DroppedUpstream)+len(got.DroppedHere) != 0 {
		t.Errorf("Classify() = %+v, want x as mine", got)
	}
}

// Somebody dropped x and published that; this clone saw x when it last
// agreed, so its copy is the dropped one, not new work of its own.
func TestACommitTheRemoteNoLongerHasIsDroppedUpstream(t *testing.T) {
	f := newFixture(t)
	f.repo.Run("branch", "synthetic-synced", "synthetic-b")
	f.repo.Run("branch", "synthetic-published", "synthetic-b~1")
	got := f.classify(t, "synthetic-b", "synthetic-published", "synthetic-synced")
	if !slices.Equal(got.DroppedUpstream, f.ids("synthetic-b")) || len(got.Mine) != 0 {
		t.Errorf("Classify() = %+v, want x dropped upstream", got)
	}
}

// This clone dropped x after agreeing on it; the remote still has it, and
// that is a drop to publish, not somebody else's commit to take.
func TestACommitThisBranchNoLongerHasIsDroppedHere(t *testing.T) {
	f := newFixture(t)
	x := f.repo.Revision("synthetic-b")
	f.repo.Run("branch", "synthetic-synced", "synthetic-b")
	f.repo.Run("branch", "synthetic-published", "synthetic-b")
	f.repo.Run("reset", "-q", "--hard", "synthetic-b~1")
	got := f.classify(t, "synthetic-b", "synthetic-published", "synthetic-synced")
	if !slices.Equal(got.DroppedHere, []string{x}) || len(got.New) != 0 {
		t.Errorf("Classify() = %+v, want x dropped here", got)
	}
}

// A commit the remote gained since the agreement is new, never a drop.
func TestACommitTheRemoteGainedIsNew(t *testing.T) {
	f := newFixture(t)
	f.repo.Run("branch", "synthetic-synced", "synthetic-b")
	f.repo.Run("switch", "-qc", "synthetic-published")
	f.repo.Commit("synthetic theirs", "theirs.txt", "theirs")
	got := f.classify(t, "synthetic-b", "synthetic-published", "synthetic-synced")
	if !slices.Equal(got.New, f.ids("synthetic-published")) || got.Dropped() {
		t.Errorf("Classify() = %+v, want their commit new", got)
	}
}

// The sync point is asked by id. A change made again, with the same content as
// one that was dropped, is a different commit: it reads as new, never as the
// dropped one coming back to be deleted.
func TestAChangeMadeAgainIsNewNotDropped(t *testing.T) {
	f := newFixture(t)
	f.repo.Run("branch", "synthetic-synced", "synthetic-b")
	f.repo.Run("switch", "-qc", "synthetic-published", "synthetic-b~1")
	if err := os.WriteFile("x.txt", []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.repo.Run("add", "x.txt")
	f.repo.Run("commit", "-qm", "synthetic x, made again")
	f.repo.Run("switch", "-q", "synthetic-b")
	f.repo.Run("reset", "-q", "--hard", "synthetic-b~1")

	got := f.classify(t, "synthetic-b", "synthetic-published", "synthetic-synced")
	if !slices.Equal(got.New, f.ids("synthetic-published")) || got.Dropped() {
		t.Errorf("Classify() = %+v, want the re-made change new", got)
	}
}

// A commit dropped from the parent, here and on the remote, is the parent's
// drop. The child carried it only because it sat on the parent, and it is
// neither side's work of the child's own.
func TestACommitDroppedFromTheParentIsNotTheChildsDrop(t *testing.T) {
	f := newFixture(t)
	// x moves down into synthetic-a, which is where it is dropped from.
	f.repo.Run("switch", "-q", "synthetic-a")
	f.repo.Commit("synthetic x on a", "xa.txt", "xa")
	f.repo.Run("switch", "-q", "synthetic-b")
	f.repo.Run("rebase", "-q", "--onto", "synthetic-a", "synthetic-a~1", "synthetic-b")
	f.repo.Run("branch", "synthetic-synced", "synthetic-b")
	f.repo.Run("branch", "synthetic-published", "synthetic-b")
	f.repo.Run("branch", "-f", "synthetic-published-a", "synthetic-a~1")
	f.repo.Run("rebase", "-q", "--onto", "synthetic-a~1", "synthetic-a", "synthetic-published")
	f.repo.Run("branch", "-f", "synthetic-a", "synthetic-a~1")
	f.repo.Run("rebase", "-q", "--onto", "synthetic-a", "synthetic-synced", "synthetic-b")

	branch := Branch{
		Local: f.repo.Revision("synthetic-b"), Remote: f.repo.Revision("synthetic-published"),
		LocalParent: f.repo.Revision("synthetic-a"), PublishedParent: f.repo.Revision("synthetic-published-a"),
		Sync: localgit.SyncPoint{Tip: f.repo.Revision("synthetic-synced")}, Synced: true,
	}
	got, err := Classify(context.Background(), f.client, branch)
	if err != nil {
		t.Fatal(err)
	}
	if got.Dropped() {
		t.Errorf("Classify() = %+v, want the parent's commit left out of the child's drops", got)
	}
}

// pull never moves the remote-tracking ref, so after a pull it is older than
// the sync point. A branch reset to it leaves out exactly what the pull brought
// down, which looks like a deliberate drop and is not safe to publish as one.
func TestAResetToAStaleTrackingRefIsNotADrop(t *testing.T) {
	f := newFixture(t)
	x := f.repo.Revision("synthetic-b")
	f.repo.Run("branch", "synthetic-synced", "synthetic-b")
	f.repo.Run("branch", "synthetic-published", "synthetic-b")
	f.repo.Run("update-ref", "refs/remotes/origin/synthetic-b", "synthetic-b~1")
	f.repo.Run("reset", "-q", "--hard", "origin/synthetic-b")

	branch := Branch{
		Local: f.repo.Revision("synthetic-b"), Remote: f.repo.Revision("synthetic-published"),
		LocalParent: f.repo.Revision("synthetic-a"), Tracking: f.repo.Revision("origin/synthetic-b"),
		Sync: localgit.SyncPoint{Tip: f.repo.Revision("synthetic-synced")}, Synced: true,
	}
	got, err := Classify(context.Background(), f.client, branch)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Stale || len(got.DroppedHere) != 0 || !slices.Equal(got.New, []string{x}) {
		t.Errorf("Classify() = %+v, want x new and the reset called stale", got)
	}
}

// Keeping a drop turns it back into work on the side that still has it, and
// says which of the named commits were this branch's drops.
func TestKeepReclassifiesOnlyDrops(t *testing.T) {
	changes := Changes{DroppedUpstream: []string{"up"}, DroppedHere: []string{"here"}, Mine: []string{"mine"}}
	kept, which := changes.Keep([]string{"up", "here", "mine"})
	if !slices.Equal(kept.Mine, []string{"mine", "up"}) || !slices.Equal(kept.New, []string{"here"}) || kept.Dropped() {
		t.Errorf("Keep() = %+v", kept)
	}
	if !slices.Equal(which, []string{"up", "here"}) {
		t.Errorf("Keep() kept %v, want the two drops and not the commit that was not one", which)
	}
}

// mayHaveDropped is the cheap question Assess asks first, and its "no" must
// never hide a drop Classify would have found. The restacked cases are what
// it exists for: the remote's version sits on the parent's old tip, so every
// one of its commits is one this branch has only under a new id.
func TestMayHaveDroppedNeverHidesADrop(t *testing.T) {
	// restack moves synthetic-a on a commit and replays synthetic-b onto it,
	// leaving the remote's version and the sync point where they were.
	restack := func(f fixture, drop bool) {
		f.repo.Run("branch", "synthetic-synced", "synthetic-b")
		f.repo.Run("branch", "synthetic-published", "synthetic-b")
		f.repo.Run("branch", "synthetic-published-a", "synthetic-a")
		f.repo.Run("switch", "-q", "synthetic-a")
		f.repo.Commit("synthetic a two", "a2.txt", "a2")
		f.repo.Run("switch", "-q", "synthetic-b")
		if drop {
			f.repo.Run("reset", "-q", "--hard", "synthetic-b~1")
		}
		f.repo.Run("rebase", "-q", "--onto", "synthetic-a", "synthetic-published-a", "synthetic-b")
	}
	tests := []struct {
		name     string
		setup    func(fixture)
		possible bool
		dropped  bool
	}{
		{name: "restacked", setup: func(f fixture) { restack(f, false) }},
		{name: "dropped while restacked", setup: func(f fixture) { restack(f, true) }, possible: true, dropped: true},
		{name: "dropped upstream", possible: true, dropped: true, setup: func(f fixture) {
			f.repo.Run("branch", "synthetic-synced", "synthetic-b")
			f.repo.Run("branch", "synthetic-published", "synthetic-b~1")
			f.repo.Run("branch", "synthetic-published-a", "synthetic-a")
		}},
		{name: "dropped here", possible: true, dropped: true, setup: func(f fixture) {
			f.repo.Run("branch", "synthetic-synced", "synthetic-b")
			f.repo.Run("branch", "synthetic-published", "synthetic-b")
			f.repo.Run("branch", "synthetic-published-a", "synthetic-a")
			f.repo.Run("reset", "-q", "--hard", "synthetic-b~1")
		}},
		// x has an equivalent in the parent now, below the bound the cheap
		// question looks within: it cannot rule the drop out, and Classify
		// finds there is none.
		{name: "landed in the parent", possible: true, setup: func(f fixture) {
			f.repo.Run("branch", "synthetic-synced", "synthetic-b")
			f.repo.Run("branch", "synthetic-published", "synthetic-b")
			f.repo.Run("branch", "synthetic-published-a", "synthetic-a")
			f.repo.Run("switch", "-q", "synthetic-a")
			f.repo.Run("cherry-pick", "synthetic-b")
			f.repo.Run("switch", "-q", "synthetic-b")
			f.repo.Run("rebase", "-q", "--onto", "synthetic-a", "synthetic-published-a", "synthetic-b")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t)
			test.setup(f)
			branch := Branch{
				Local: f.repo.Revision("synthetic-b"), Remote: f.repo.Revision("synthetic-published"),
				LocalParent: f.repo.Revision("synthetic-a"), PublishedParent: f.repo.Revision("synthetic-published-a"),
				Sync: localgit.SyncPoint{Tip: f.repo.Revision("synthetic-synced")}, Synced: true,
			}
			possible, err := mayHaveDropped(context.Background(), f.client, branch)
			if err != nil {
				t.Fatal(err)
			}
			changes, err := Classify(context.Background(), f.client, branch)
			if err != nil {
				t.Fatal(err)
			}
			if possible != test.possible {
				t.Errorf("mayHaveDropped() = %v, want %v (Classify() = %+v)", possible, test.possible, changes)
			}
			if changes.Dropped() != test.dropped {
				t.Errorf("Classify() = %+v, want dropped %v", changes, test.dropped)
			}
		})
	}
}

// replay makes branch from base with x replayed onto it after a conflict was
// resolved: the same author, author date and subject, a different patch.
func (f fixture) replay(branch, base string) {
	f.repo.Run("switch", "-qc", branch, base)
	f.repo.Run("cherry-pick", "synthetic-b")
	f.repo.Write("x.txt", "x, as the conflict was resolved")
	f.repo.Run("add", "x.txt")
	f.repo.Run("commit", "-q", "--amend", "--no-edit")
}

// A replay that resolved a conflict changed x's patch, so by content it
// matches nothing published. It is x replayed, not x dropped and another
// commit made, and saying the second is what refused to publish it. Both
// commits stay where a reader that knows nothing of pairs would look -- the
// old one dropped, the replay new -- so that reader refuses rather than
// publishes.
func TestACommitReplayedWithAResolvedConflictIsReplacedHere(t *testing.T) {
	f := newFixture(t)
	f.repo.Run("branch", "synthetic-synced", "synthetic-b")
	f.repo.Run("branch", "synthetic-published", "synthetic-b")
	f.replay("synthetic-local", "synthetic-b~1")
	got := f.classify(t, "synthetic-local", "synthetic-published", "synthetic-synced")
	old, replay := f.repo.Revision("synthetic-b"), f.repo.Revision("synthetic-local")
	if !slices.Equal(got.ReplacedHere, []Replacement{{Old: old, New: replay}}) || len(got.ReplacedUpstream) != 0 {
		t.Errorf("Classify() = %+v, want x replaced here", got)
	}
	if !slices.Equal(got.DroppedHere, []string{old}) || !slices.Equal(got.Mine, []string{replay}) || len(got.New)+len(got.DroppedUpstream) != 0 {
		t.Errorf("Classify() = %+v, want x dropped here and its replay mine", got)
	}
	if unpaired := got.Unpaired(slices.Concat(got.DroppedHere, got.Mine)); len(unpaired) != 0 {
		t.Errorf("Unpaired() = %v, want the pair excluded", unpaired)
	}
}

// The same from the other side: somebody resolved a conflict replaying x and
// published it.
func TestACommitTheRemoteReplayedIsReplacedUpstream(t *testing.T) {
	f := newFixture(t)
	f.repo.Run("branch", "synthetic-synced", "synthetic-b")
	f.replay("synthetic-published", "synthetic-b~1")
	got := f.classify(t, "synthetic-b", "synthetic-published", "synthetic-synced")
	old, replay := f.repo.Revision("synthetic-b"), f.repo.Revision("synthetic-published")
	if !slices.Equal(got.ReplacedUpstream, []Replacement{{Old: old, New: replay}}) || len(got.ReplacedHere) != 0 {
		t.Errorf("Classify() = %+v, want x replaced upstream", got)
	}
	if !slices.Equal(got.DroppedUpstream, []string{old}) || !slices.Equal(got.New, []string{replay}) || len(got.Mine)+len(got.DroppedHere) != 0 {
		t.Errorf("Classify() = %+v, want x dropped upstream and the remote's replay new", got)
	}
}

// Without a sync point there is no telling the remote's x from a reviewer's
// amend of it, so nothing pairs and the difference stays new on both sides.
func TestWithoutASyncPointNothingIsReplaced(t *testing.T) {
	f := newFixture(t)
	f.repo.Run("branch", "synthetic-published", "synthetic-b")
	f.replay("synthetic-local", "synthetic-b~1")
	got := f.classify(t, "synthetic-local", "synthetic-published", "")
	if len(got.ReplacedHere)+len(got.ReplacedUpstream) != 0 || len(got.Mine) != 1 || len(got.New) != 1 {
		t.Errorf("Classify() = %+v, want x new on both sides", got)
	}
}

// Pairing is one to one and forward in time. A key shared by two commits on
// either side is ambiguous, and a "replay" older than what it replaces is a
// return to the version before somebody else's replay.
func TestPairsMatchOnlyOneLaterCommitPerKey(t *testing.T) {
	carried := map[string]localgit.Carried{
		"old-a": {Key: "a", Committed: 10}, "new-a": {Key: "a", Committed: 20},
		"old-b": {Key: "b", Committed: 10}, "new-b1": {Key: "b", Committed: 20}, "new-b2": {Key: "b", Committed: 20},
		"old-c": {Key: "c", Committed: 30}, "new-c": {Key: "c", Committed: 20},
		"old-d": {Key: "d", Committed: 10}, "new-e": {Key: "e", Committed: 20},
	}
	got := pairs([]string{"old-a", "old-b", "old-c", "old-d"}, []string{"new-a", "new-b1", "new-b2", "new-c", "new-e"}, carried)
	if want := []Replacement{{Old: "old-a", New: "new-a"}}; !slices.Equal(got, want) {
		t.Errorf("pairs() = %+v, want %+v", got, want)
	}
}

// Keeping the old commit of a pair undoes the pairing: it is one more drop to
// keep, and the version the replay made one more commit of its side.
func TestKeepingAReplacedCommitUndoesThePair(t *testing.T) {
	changes := Changes{
		DroppedHere: []string{"old", "other"}, Mine: []string{"new", "made"},
		ReplacedHere: []Replacement{{Old: "old", New: "new"}, {Old: "other", New: "made"}},
	}
	got, kept := changes.Keep([]string{"old"})
	if !slices.Equal(kept, []string{"old"}) {
		t.Errorf("kept = %v, want old", kept)
	}
	if !slices.Equal(got.New, []string{"old"}) || !slices.Equal(got.DroppedHere, []string{"other"}) ||
		!slices.Equal(got.Mine, []string{"new", "made"}) || !slices.Equal(got.ReplacedHere, []Replacement{{Old: "other", New: "made"}}) {
		t.Errorf("Keep() = %+v, want old taken back, new mine, the other pair kept", got)
	}
	if unpaired := got.Unpaired(got.Mine); !slices.Equal(unpaired, []string{"new"}) {
		t.Errorf("Unpaired(Mine) = %v, want new no longer a replay", unpaired)
	}
}

// The same from the remote's side: keeping a commit the remote replayed makes
// it this clone's to publish again, and the remote's replay one more new
// commit there. The two sides go through one helper with their lists swapped,
// and swapping them the wrong way round is what this would catch.
func TestKeepingACommitTheRemoteReplayedUndoesThePair(t *testing.T) {
	changes := Changes{
		DroppedUpstream: []string{"old", "other"}, New: []string{"new", "made"},
		ReplacedUpstream: []Replacement{{Old: "old", New: "new"}, {Old: "other", New: "made"}},
	}
	got, kept := changes.Keep([]string{"old"})
	if !slices.Equal(kept, []string{"old"}) {
		t.Errorf("kept = %v, want old", kept)
	}
	if !slices.Equal(got.Mine, []string{"old"}) || !slices.Equal(got.New, []string{"new", "made"}) || !slices.Equal(got.DroppedUpstream, []string{"other"}) ||
		!slices.Equal(got.ReplacedUpstream, []Replacement{{Old: "other", New: "made"}}) || len(got.ReplacedHere) != 0 {
		t.Errorf("Keep() = %+v, want old mine again, new theirs, the other pair kept", got)
	}
}

// Unpaired takes both commits of every pair out of whichever list it is
// given, from either side, and leaves everything else in order.
func TestUnpairedLeavesOnlyCommitsNoReplayExplains(t *testing.T) {
	changes := Changes{
		DroppedHere: []string{"replaced-here", "dropped-here"}, Mine: []string{"mine", "replay-here"},
		DroppedUpstream: []string{"dropped-there", "replaced-there"}, New: []string{"replay-there", "new"},
		ReplacedHere:     []Replacement{{Old: "replaced-here", New: "replay-here"}},
		ReplacedUpstream: []Replacement{{Old: "replaced-there", New: "replay-there"}},
	}
	for _, test := range []struct {
		name       string
		list, want []string
	}{
		{"dropped here", changes.DroppedHere, []string{"dropped-here"}},
		{"mine", changes.Mine, []string{"mine"}},
		{"dropped upstream", changes.DroppedUpstream, []string{"dropped-there"}},
		{"new", changes.New, []string{"new"}},
	} {
		if got := changes.Unpaired(test.list); !slices.Equal(got, test.want) {
			t.Errorf("Unpaired(%s) = %v, want %v", test.name, got, test.want)
		}
	}
	if !changes.Dropped() || !(Changes{DroppedHere: []string{"x"}, ReplacedHere: []Replacement{{Old: "x", New: "y"}}}).Dropped() {
		t.Error("Dropped() = false, want a replaced commit to count as one")
	}
	if got := changes.ReplacedHere.Named("synthetic-b"); !slices.Equal(got, []Drop{{Kind: Replaced, Branch: "synthetic-b", Commit: "replaced-here", To: "replay-here"}}) {
		t.Errorf("Named() = %+v", got)
	}
}

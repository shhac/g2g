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

package restack

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type fakeLocator struct{ dir string }

func (f fakeLocator) CommonDir(context.Context) (string, error) { return f.dir, nil }

func newJournal(t *testing.T) (FileJournal, string) {
	t.Helper()
	common := t.TempDir()
	return FileJournal{Git: fakeLocator{dir: common}}, common
}

func TestJournalStartIsExclusiveAcrossConcurrentCallers(t *testing.T) {
	journal, common := newJournal(t)
	ctx := context.Background()
	const callers = 8
	results := make(chan error, callers)
	var group sync.WaitGroup
	for range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			results <- journal.Start(ctx, Record{Branch: "synthetic-a"})
		}()
	}
	group.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !strings.Contains(err.Error(), "already in progress") {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatalf("%d starts succeeded, want exactly one", successes)
	}
	loaded, found, err := journal.Load(ctx)
	if err != nil || !found || loaded.Branch != "synthetic-a" {
		t.Fatalf("journal = %+v, %v, %v", loaded, found, err)
	}
	entries, err := os.ReadDir(filepath.Join(common, "g2g"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary files leaked: %v, %v", entries, err)
	}
}

// Nothing in flight is the ordinary state, not an error.
func TestJournalLoadOfAnAbsentRecord(t *testing.T) {
	journal, _ := newJournal(t)

	_, found, err := journal.Load(context.Background())
	if err != nil || found {
		t.Fatalf("Load() = %t, %v; want not found and no error", found, err)
	}
}

func TestJournalRoundTripsTheOriginalTips(t *testing.T) {
	journal, _ := newJournal(t)
	ctx := context.Background()
	record := Record{
		Branch:         "synthetic-b",
		Scope:          "graph",
		ReturnTo:       "synthetic-b",
		Worktree:       "/synthetic/git/worktrees/checkout",
		CheckoutBranch: "synthetic-b",
		CheckoutTip:    "bbb",
		Original:       map[string]string{"synthetic-a": "aaa", "synthetic-b": "bbb"},
		Structure: map[string]RecordedEdge{
			"synthetic-b": {Parent: "synthetic-a", ForkPoint: "aaa"},
		},
	}

	if err := journal.Save(ctx, record); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	loaded, found, err := journal.Load(ctx)
	if err != nil || !found {
		t.Fatalf("Load() = %t, %v", found, err)
	}
	if loaded.Original["synthetic-a"] != "aaa" || loaded.Original["synthetic-b"] != "bbb" {
		t.Errorf("Original = %v; without these --abort cannot restore anything", loaded.Original)
	}
	if got := loaded.Structure["synthetic-b"]; got != (RecordedEdge{Parent: "synthetic-a", ForkPoint: "aaa"}) {
		t.Errorf("Structure = %v; without it --abort leaves the fork points a resume recorded", loaded.Structure)
	}
	if loaded.Selection().Branch != "synthetic-b" {
		t.Errorf("Selection() = %#v", loaded.Selection())
	}
	if loaded.Worktree != record.Worktree || loaded.CheckoutBranch != record.CheckoutBranch || loaded.CheckoutTip != record.CheckoutTip {
		t.Errorf("checkout recovery state lost: %+v", loaded)
	}
}

// A half-written journal would block every later command, so the write is
// atomic and leaves nothing behind.
func TestJournalWritesAtomically(t *testing.T) {
	journal, common := newJournal(t)
	ctx := context.Background()

	if err := journal.Save(ctx, Record{Original: map[string]string{"a": "1"}}); err != nil {
		t.Fatal(err)
	}
	if err := journal.Save(ctx, Record{Original: map[string]string{"a": "2"}}); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(filepath.Join(common, "g2g"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "restack.json" {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Errorf("journal directory = %v, want only restack.json", names)
	}
}

func TestJournalClearIsHarmlessWhenNothingIsInFlight(t *testing.T) {
	journal, _ := newJournal(t)

	if err := journal.Clear(context.Background()); err != nil {
		t.Errorf("Clear() error = %v with nothing to clear", err)
	}
}

// A journal written by a newer g2g must not be acted on by an older one: it
// describes a rewrite this build may not know how to finish.
func TestJournalFailsClosedOnAnUnsupportedSchema(t *testing.T) {
	journal, common := newJournal(t)
	dir := filepath.Join(common, "g2g")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "restack.json"), []byte(`{"schemaVersion":99}`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := journal.Load(context.Background())
	if err == nil {
		t.Fatal("Load() error = nil for a future schema version")
	}
	if !strings.Contains(err.Error(), "99") {
		t.Errorf("error = %v, want it to name the version found", err)
	}
}

func TestJournalRequiresALocator(t *testing.T) {
	if _, _, err := (FileJournal{}).Load(context.Background()); err == nil {
		t.Error("Load() error = nil without a locator")
	}
	if err := (FileJournal{}).Save(context.Background(), Record{}); err == nil {
		t.Error("Save() error = nil without a locator")
	}
}

func TestReplayBoundaryRequiresVersionTwoButOrdinaryJournalsStayCompatible(t *testing.T) {
	journal, _ := newJournal(t)
	for _, above := range []string{"", "synthetic-a"} {
		if err := journal.Save(context.Background(), Record{Branch: "synthetic-b", Above: above}); err != nil {
			t.Fatal(err)
		}
		loaded, found, err := journal.Load(context.Background())
		want := 1
		if above != "" {
			want = 2
		}
		if err != nil || !found || loaded.SchemaVersion != want || loaded.Above != above {
			t.Fatalf("loaded=%+v found=%t err=%v", loaded, found, err)
		}
	}
}

// worktreeLocator answers which private Git directory a worktree runs in.
type worktreeLocator struct{ common, dir string }

func (l worktreeLocator) CommonDir(context.Context) (string, error)   { return l.common, nil }
func (l worktreeLocator) WorktreeDir(context.Context) (string, error) { return l.dir, nil }

// Each worktree journals its own restack, so one stopped in a linked worktree
// is that worktree's and the main worktree's is the main worktree's, and each
// sees the other's as somebody else's.
func TestEachWorktreeJournalsItsOwnRestack(t *testing.T) {
	ctx := context.Background()
	common := t.TempDir()
	linkedDir := filepath.Join(common, "worktrees", "synthetic-agent")
	main := FileJournal{Git: worktreeLocator{common: common, dir: common}}
	linked := FileJournal{Git: worktreeLocator{common: common, dir: linkedDir}}

	if err := linked.Start(ctx, Record{Branch: "synthetic-busy", Worktree: linkedDir}); err != nil {
		t.Fatal(err)
	}
	if err := main.Start(ctx, Record{Branch: "synthetic-free", Worktree: common}); err != nil {
		t.Fatalf("the main worktree could not start a restack beside the linked one's: %v", err)
	}
	if _, err := os.Stat(filepath.Join(common, "g2g", "restack", "worktrees", "synthetic-agent.json")); err != nil {
		t.Fatalf("the linked worktree's journal is not where it belongs: %v", err)
	}
	for _, test := range []struct {
		journal     FileJournal
		mine, other string
	}{
		{main, "synthetic-free", "synthetic-busy"},
		{linked, "synthetic-busy", "synthetic-free"},
	} {
		record, found, err := test.journal.Load(ctx)
		if err != nil || !found || record.Branch != test.mine {
			t.Errorf("own journal = %+v, %v, %v, want %s", record, found, err, test.mine)
		}
		others, err := test.journal.Others(ctx)
		if err != nil || len(others) != 1 || others[0].Branch != test.other {
			t.Errorf("others = %+v, %v, want only %s", others, err, test.other)
		}
	}
	if err := linked.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := main.Load(ctx); !found {
		t.Error("clearing the linked worktree's journal cleared the main worktree's")
	}
}

// A restack an older g2g journaled for a linked worktree is in the shared
// file. It stays that worktree's to finish there, and the main worktree,
// whose file it is in, sees it as another's and never clears it.
func TestASharedJournalFromBeforeStaysItsWorktrees(t *testing.T) {
	ctx := context.Background()
	common := t.TempDir()
	linkedDir := filepath.Join(common, "worktrees", "synthetic-agent")
	legacy := FileJournal{Git: fakeLocator{dir: common}}
	if err := legacy.Start(ctx, Record{Branch: "synthetic-busy", Worktree: linkedDir}); err != nil {
		t.Fatal(err)
	}
	main := FileJournal{Git: worktreeLocator{common: common, dir: common}}
	linked := FileJournal{Git: worktreeLocator{common: common, dir: linkedDir}}

	if record, found, err := linked.Load(ctx); err != nil || !found || record.Branch != "synthetic-busy" {
		t.Fatalf("the linked worktree lost its restack: %+v, %v, %v", record, found, err)
	}
	if _, found, err := main.Load(ctx); err != nil || found {
		t.Fatalf("the main worktree took the linked one's restack for its own: %v, %v", found, err)
	}
	if others, err := main.Others(ctx); err != nil || len(others) != 1 || others[0].Branch != "synthetic-busy" {
		t.Fatalf("the main worktree does not see the linked one's restack: %+v, %v", others, err)
	}
	if err := main.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	record, found, err := linked.Load(ctx)
	if err != nil || !found {
		t.Fatalf("the main worktree cleared the linked one's restack: %+v, %v, %v", record, found, err)
	}
	record.ReturnTo = "synthetic-busy"
	if err := linked.Save(ctx, record); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(common, "g2g", "restack", "worktrees", "synthetic-agent.json")); !os.IsNotExist(err) {
		t.Errorf("a save moved the restack out of the file it was started in: %v", err)
	}
	if err := linked.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := main.Load(ctx); found {
		t.Error("the shared file outlived its restack")
	}
	if others, _ := main.Others(ctx); len(others) != 0 {
		t.Errorf("others after the restack finished = %+v", others)
	}
}

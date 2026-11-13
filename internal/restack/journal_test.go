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
		others, err := test.journal.Elsewhere(ctx)
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
	if others, err := main.Elsewhere(ctx); err != nil || len(others) != 1 || others[0].Branch != "synthetic-busy" {
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
	if others, _ := main.Elsewhere(ctx); len(others) != 0 {
		t.Errorf("others after the restack finished = %+v", others)
	}
}

// Whose a record is goes by the file it is in, never by the absolute path
// inside it. A repository moved part-way through a restack leaves that path
// stale, and reading it as somebody else's told the reader to delete the
// journal their own --abort needs.
func TestARecordIsTheWorktreesWhoseFileItIsIn(t *testing.T) {
	ctx := context.Background()
	common := t.TempDir()
	linkedDir := filepath.Join(common, "worktrees", "synthetic-agent")
	main := FileJournal{Git: worktreeLocator{common: common, dir: common}}
	linked := FileJournal{Git: worktreeLocator{common: common, dir: linkedDir}}

	if err := main.Start(ctx, Record{Branch: "synthetic-main-work", Worktree: "/synthetic/moved-away/.git"}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := main.Load(ctx); err != nil || !found {
		t.Fatalf("the main worktree lost its own restack once its path went stale: %v, %v", found, err)
	}
	if err := os.MkdirAll(linkedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	others, err := linked.Elsewhere(ctx)
	if err != nil || len(others) != 1 || others[0].Gone || others[0].GitDir != common {
		t.Fatalf("from a linked worktree the main worktree's restack = %+v, %v, want it there and not gone", others, err)
	}
}

// A record from before any said which worktree it belongs to is anyone's, as
// it was then: every worktree finds it its own, so each refuses as busy and
// any may finish it, and none is told it is somebody else's to delete.
func TestARecordNamingNoWorktreeIsAnyonesAsBefore(t *testing.T) {
	ctx := context.Background()
	common := t.TempDir()
	if err := (FileJournal{Git: fakeLocator{dir: common}}).Start(ctx, Record{Branch: "synthetic-old"}); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{common, filepath.Join(common, "worktrees", "synthetic-agent")} {
		journal := FileJournal{Git: worktreeLocator{common: common, dir: dir}}
		here, found, err := journal.Load(ctx)
		others, othersErr := journal.Elsewhere(ctx)
		if err != nil || othersErr != nil || !found || here.Branch != "synthetic-old" || len(others) != 0 {
			t.Errorf("from %s: here = %+v (%t), others = %+v, %v %v, want it as this worktree's own", dir, here, found, others, err, othersErr)
		}
	}
}

// Another worktree is gone only when Git has no private directory for it,
// which removing a worktree takes. Its checkout is read from the backlink Git
// keeps there, relative or absolute, and a checkout that cannot be found is
// named by its Git directory rather than taken as gone.
func TestAnotherWorktreeIsGoneOnlyWithoutItsPrivateDirectory(t *testing.T) {
	ctx := context.Background()
	common := t.TempDir()
	checkouts := t.TempDir()
	main := FileJournal{Git: worktreeLocator{common: common, dir: common}}
	start := func(name string) {
		t.Helper()
		dir := filepath.Join(common, "worktrees", name)
		if err := (FileJournal{Git: worktreeLocator{common: common, dir: dir}}).Start(ctx, Record{Branch: name, Worktree: dir}); err != nil {
			t.Fatal(err)
		}
	}
	backlink := func(name, link string) {
		t.Helper()
		dir := filepath.Join(common, "worktrees", name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if link == "" {
			return
		}
		if err := os.WriteFile(filepath.Join(dir, "gitdir"), []byte(link+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	absolute := filepath.Join(checkouts, "synthetic-absolute")
	if err := os.MkdirAll(filepath.Join(absolute, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	backlink("synthetic-absolute", filepath.Join(absolute, ".git"))
	relativeCheckout := filepath.Join(common, "synthetic-relative")
	if err := os.MkdirAll(filepath.Join(relativeCheckout, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	backlink("synthetic-relative", filepath.Join("..", "..", "synthetic-relative", ".git"))
	backlink("synthetic-unlinked", "")
	for _, name := range []string{"synthetic-absolute", "synthetic-relative", "synthetic-unlinked", "synthetic-removed"} {
		start(name)
	}

	others, err := main.Elsewhere(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Elsewhere{}
	for _, other := range others {
		got[other.Branch] = other
	}
	for name, want := range map[string]struct {
		checkout string
		gone     bool
	}{
		"synthetic-absolute": {checkout: absolute},
		"synthetic-relative": {checkout: relativeCheckout},
		"synthetic-unlinked": {},
		"synthetic-removed":  {gone: true},
	} {
		if got[name].Checkout != want.checkout || got[name].Gone != want.gone {
			t.Errorf("%s: checkout %q gone %t, want %q %t", name, got[name].Checkout, got[name].Gone, want.checkout, want.gone)
		}
	}
}

// A main worktree whose checkout is itself named worktrees keeps its own
// restack: its private directory is the common one, never a linked
// worktree's called .git, which read as gone and offered to delete the
// journal its --abort needs.
func TestAMainWorktreeInADirectoryNamedWorktreesKeepsItsRestack(t *testing.T) {
	ctx := context.Background()
	common := filepath.Join(t.TempDir(), "worktrees", ".git")
	main := FileJournal{Git: worktreeLocator{common: common, dir: common}}
	if err := main.Start(ctx, Record{Branch: "synthetic-main-work", Worktree: common}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := main.Load(ctx); err != nil || !found {
		t.Fatalf("the main worktree lost its own restack: %v, %v", found, err)
	}
	linked := FileJournal{Git: worktreeLocator{common: common, dir: filepath.Join(common, "worktrees", "synthetic-agent")}}
	others, err := linked.Elsewhere(ctx)
	if err != nil || len(others) != 1 || others[0].Gone {
		t.Fatalf("from a linked worktree the main worktree's restack = %+v, %v, want it there and not gone", others, err)
	}
}

// A repository whose path has a bracket in it lists its worktrees' journals
// all the same. They were found with a pattern built from that path, which
// read the bracket as a pattern and saw none, or refused to parse it.
func TestAnotherWorktreesJournalIsFoundWhateverTheRepositoryIsCalled(t *testing.T) {
	ctx := context.Background()
	for _, name := range []string{"synthetic [old]", "synthetic[1]", "synthetic[open"} {
		t.Run(name, func(t *testing.T) {
			common := filepath.Join(t.TempDir(), name, ".git")
			linkedDir := filepath.Join(common, "worktrees", "synthetic-agent")
			linked := FileJournal{Git: worktreeLocator{common: common, dir: linkedDir}}
			if err := linked.Start(ctx, Record{Branch: "synthetic-busy", Worktree: linkedDir}); err != nil {
				t.Fatal(err)
			}
			main := FileJournal{Git: worktreeLocator{common: common, dir: common}}
			others, err := main.Elsewhere(ctx)
			if err != nil || len(others) != 1 || others[0].Branch != "synthetic-busy" {
				t.Errorf("others = %+v, %v, want the linked worktree's restack", others, err)
			}
		})
	}
}

// Another worktree's damaged journal stops only the guards that weigh it,
// never this worktree's own restack.
func TestAnotherWorktreesDamagedJournalLeavesThisOnesAlone(t *testing.T) {
	ctx := context.Background()
	common := t.TempDir()
	linkedDir := filepath.Join(common, "worktrees", "synthetic-agent")
	linked := FileJournal{Git: worktreeLocator{common: common, dir: linkedDir}}
	if err := linked.Start(ctx, Record{Branch: "synthetic-own", Worktree: linkedDir}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(common, "g2g", journalFileName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if record, found, err := linked.Load(ctx); err != nil || !found || record.Branch != "synthetic-own" {
		t.Errorf("the linked worktree's own restack = %+v, %v, %v", record, found, err)
	}
	if _, err := linked.Elsewhere(ctx); err == nil {
		t.Error("the damaged journal was weighed as nothing")
	}
}

// Clearing from a linked worktree when the shared file is damaged removes
// that worktree's own journal and nothing else: the damaged file is the main
// worktree's rollback state.
func TestClearingBesideADamagedSharedJournalRemovesOnlyThisWorktreesOwn(t *testing.T) {
	ctx := context.Background()
	common := t.TempDir()
	linkedDir := filepath.Join(common, "worktrees", "synthetic-agent")
	linked := FileJournal{Git: worktreeLocator{common: common, dir: linkedDir}}
	if err := linked.Start(ctx, Record{Branch: "synthetic-mine", Worktree: linkedDir}); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(common, "g2g", journalFileName)
	if err := os.WriteFile(shared, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := linked.Clear(ctx); err != nil {
		t.Fatalf("Clear = %v", err)
	}
	if _, err := os.Stat(filepath.Join(common, "g2g", "restack", "worktrees", "synthetic-agent.json")); !os.IsNotExist(err) {
		t.Errorf("the linked worktree's own journal survived: %v", err)
	}
	if _, err := os.Stat(shared); err != nil {
		t.Errorf("the damaged shared journal was removed by another worktree: %v", err)
	}
}

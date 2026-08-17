package restack

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeLocator struct{ dir string }

func (f fakeLocator) CommonDir(context.Context) (string, error) { return f.dir, nil }

func newJournal(t *testing.T) (FileJournal, string) {
	t.Helper()
	common := t.TempDir()
	return FileJournal{Git: fakeLocator{dir: common}}, common
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
		Branch:   "synthetic-b",
		Scope:    "graph",
		ReturnTo: "synthetic-b",
		Original: map[string]string{"synthetic-a": "aaa", "synthetic-b": "bbb"},
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
	if loaded.Selection().Branch != "synthetic-b" {
		t.Errorf("Selection() = %#v", loaded.Selection())
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

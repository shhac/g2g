package graph

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeLocator stands in for the Git common directory. Injecting it keeps every
// store test off the process's real repository.
type fakeLocator struct {
	dir string
	err error
}

func (f fakeLocator) CommonDir(context.Context) (string, error) { return f.dir, f.err }

func newStore(t *testing.T) (FileStore, string) {
	t.Helper()
	common := t.TempDir()
	return FileStore{Git: fakeLocator{dir: common}}, common
}

func TestLoadOfAnAbsentStoreIsAnEmptyGraph(t *testing.T) {
	store, _ := newStore(t)

	loaded, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v; not having adopted anything is the normal starting state", err)
	}
	if len(loaded.Edges) != 0 {
		t.Errorf("Load() = %#v, want an empty graph", loaded)
	}
}

func TestSaveThenLoadRoundTripsTheForest(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()

	if err := store.Save(ctx, forest()); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !loaded.Equal(forest()) {
		t.Errorf("round trip = %#v, want %#v", loaded, forest())
	}
	if edge := loaded.Edges["synthetic-login"]; edge.Origin != OriginAncestry {
		t.Errorf("edge = %#v, want the origin preserved", edge)
	}
}

func TestStorePathIsUnderTheGitCommonDirectory(t *testing.T) {
	store, common := newStore(t)

	path, err := store.Path(context.Background())
	if err != nil {
		t.Fatalf("Path() error = %v", err)
	}
	if want := filepath.Join(common, "g2g", "graph.json"); path != want {
		t.Errorf("Path() = %q, want %q", path, want)
	}
}

// Reading a newer store optimistically is how an older g2g silently
// rewrites structure it did not understand.
func TestLoadFailsClosedOnAnUnsupportedSchemaVersion(t *testing.T) {
	store, common := newStore(t)
	writeStore(t, common, `{"storeSchemaVersion":99,"branches":{}}`)

	_, err := store.Load(context.Background())
	if err == nil {
		t.Fatal("Load() error = nil for a future schema version")
	}
	if !strings.Contains(err.Error(), "99") {
		t.Errorf("Load() error = %v, want it to name the version found", err)
	}
}

func TestLoadRejectsAStoreThatIsNotAForest(t *testing.T) {
	store, common := newStore(t)
	writeStore(t, common, `{"storeSchemaVersion":1,"branches":{
		"synthetic-a":{"parent":"synthetic-b"},
		"synthetic-b":{"parent":"synthetic-a"}}}`)

	if _, err := store.Load(context.Background()); err == nil {
		t.Fatal("Load() error = nil for a cycle on disk")
	}
}

func TestLoadReportsUnreadableJSONWithItsPath(t *testing.T) {
	store, common := newStore(t)
	writeStore(t, common, `{not json`)

	_, err := store.Load(context.Background())
	if err == nil || !strings.Contains(err.Error(), "graph.json") {
		t.Fatalf("Load() error = %v, want it to name the file", err)
	}
}

func TestSaveRefusesToWriteAGraphThatIsNotAForest(t *testing.T) {
	store, common := newStore(t)
	cyclic := Graph{Edges: map[string]Edge{
		"synthetic-a": {Parent: "synthetic-b"},
		"synthetic-b": {Parent: "synthetic-a"},
	}}

	if err := store.Save(context.Background(), cyclic); err == nil {
		t.Fatal("Save() error = nil for a cycle")
	}
	if _, err := os.Stat(filepath.Join(common, "g2g", "graph.json")); err == nil {
		t.Error("Save() wrote a file it had rejected")
	}
}

// The rename is what makes a concurrent reader see either the old graph or the
// new one. A leftover temporary file would mean the write was not atomic.
func TestSaveLeavesNoTemporaryFilesBehind(t *testing.T) {
	store, common := newStore(t)
	ctx := context.Background()

	if err := store.Save(ctx, forest()); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, forest().Untrack("synthetic-billing")); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(filepath.Join(common, "g2g"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "graph.json" {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Errorf("store directory = %v, want only graph.json", names)
	}
}

func TestSaveOverwritesRatherThanMerging(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()

	if err := store.Save(ctx, forest()); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, forest().Untrack("synthetic-login")); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Tracked("synthetic-login") {
		t.Error("Save() merged with the previous contents instead of replacing them")
	}
}

func TestStoreRequiresALocator(t *testing.T) {
	if _, err := (FileStore{}).Load(context.Background()); err == nil {
		t.Error("Load() error = nil without a locator")
	}
	if err := (FileStore{}).Save(context.Background(), New()); err == nil {
		t.Error("Save() error = nil without a locator")
	}
}

func writeStore(t *testing.T, common, contents string) {
	t.Helper()
	dir := filepath.Join(common, "g2g")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "graph.json"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

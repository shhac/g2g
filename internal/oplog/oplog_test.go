package oplog

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/shhac/g2g/internal/graph"
)

func reading() Reading {
	return Reading{
		Refs: map[string]string{
			"refs/heads/synthetic-a":              "a-1",
			"refs/heads/synthetic-other":          "other-1",
			"refs/g2g/forkpoints/synthetic-a":     "main-1",
			"refs/g2g/synced/origin/synthetic-a":  "a-1",
			"refs/g2g/synced/origin/synthetic/sl": "sl-1",
		},
		Graph: graph.Graph{Trunks: []string{"synthetic-main"}, Edges: map[string]graph.Edge{
			"synthetic-a":     {Parent: "synthetic-main", ForkPoint: "main-1"},
			"synthetic-other": {Parent: "synthetic-main"},
		}},
		Here: "refs/heads/synthetic-a",
	}
}

// An entry is what changed between two readings, cut to what the operation
// says it touches: another session's branch moving in the same seconds is no
// part of it.
func TestAnEntryIsTheDifferenceCutToWhatTheOperationTouches(t *testing.T) {
	after := reading()
	after.Refs["refs/heads/synthetic-a"] = "a-2"
	after.Refs["refs/g2g/forkpoints/synthetic-a"] = "main-2"
	after.Refs["refs/heads/synthetic-other"] = "other-2"
	after.Refs["refs/heads/synthetic-new"] = "new-1"
	delete(after.Refs, "refs/g2g/synced/origin/synthetic-a")
	after.Graph.Edges["synthetic-a"] = graph.Edge{Parent: "synthetic-main", ForkPoint: "main-2"}
	after.Graph.Edges["synthetic-other"] = graph.Edge{Parent: "synthetic-a"}
	after.Here = "refs/heads/synthetic-new"

	entry := Between(reading(), after, func(branch string) bool { return branch == "synthetic-a" || branch == "synthetic-new" })
	want := map[string]Move{
		"refs/heads/synthetic-a":             {Before: "a-1", After: "a-2"},
		"refs/g2g/forkpoints/synthetic-a":    {Before: "main-1", After: "main-2"},
		"refs/g2g/synced/origin/synthetic-a": {Before: "a-1"},
		"refs/heads/synthetic-new":           {After: "new-1"},
	}
	if len(entry.Refs) != len(want) {
		t.Fatalf("refs = %v, want %v", entry.Refs, want)
	}
	for ref, move := range want {
		if entry.Refs[ref] != move {
			t.Errorf("%s = %+v, want %+v", ref, entry.Refs[ref], move)
		}
	}
	if len(entry.Edges) != 1 || entry.Edges["synthetic-a"].Before.ForkPoint != "main-1" || entry.Edges["synthetic-a"].After.ForkPoint != "main-2" {
		t.Errorf("edges = %+v, want only synthetic-a's", entry.Edges)
	}
	if entry.Here == nil || entry.Here.Before != "refs/heads/synthetic-a" || entry.Here.After != "refs/heads/synthetic-new" {
		t.Errorf("here = %+v", entry.Here)
	}
	if entry.RootsBefore != nil {
		t.Error("the trunks did not change and were recorded")
	}

	if all := Between(reading(), after, nil); len(all.Refs) != 5 || len(all.Edges) != 2 {
		t.Errorf("with nothing to cut by: %d refs and %d edges, want everything that changed", len(all.Refs), len(all.Edges))
	}
	if !Between(reading(), reading(), nil).Empty() {
		t.Error("two readings that agree made an entry")
	}
}

// A ref is about the branch it is named for, however deep that name is, and
// a sync point may be about any branch its name ends in, since the remote it
// starts with may hold a slash too.
func TestBranchesOfNamesTheBranchesARefMayBeAbout(t *testing.T) {
	for ref, want := range map[string][]string{
		"refs/heads/synthetic/deep":               {"synthetic/deep"},
		"refs/g2g/forkpoints/synthetic/deep":      {"synthetic/deep"},
		"refs/g2g/synced/origin/synthetic-a":      {"synthetic-a"},
		"refs/g2g/synced/team/shared/synthetic-a": {"shared/synthetic-a", "synthetic-a"},
		"refs/g2g/synced/origin":                  {},
		"refs/g2g/remotes/origin/synthetic-main":  nil,
		"refs/tags/synthetic":                     nil,
	} {
		if got := BranchesOf(ref); !slices.Equal(got, want) {
			t.Errorf("BranchesOf(%q) = %q, want %q", ref, got, want)
		}
	}
}

// The newest operation not yet put back is the one offered, with what has
// been published since. A log that cannot be read holds nothing.
func TestTheLogOffersTheNewestOperationNotUndone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "g2g", "operations.json")
	start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	move := map[string]Move{"refs/heads/synthetic-a": {Before: "a-1", After: "a-2"}}
	for index, entry := range []Entry{
		{Command: "g2g first", Refs: move},
		{Command: "g2g second", Refs: move},
		{Command: "g2g push", Published: []string{"synthetic-a"}},
	} {
		entry.At = start.Add(time.Duration(index) * time.Minute)
		if err := Append(path, entry, entry.At); err != nil {
			t.Fatal(err)
		}
	}
	entry, published, found := Load(path).Latest(start.Add(time.Hour))
	if !found || entry.Command != "g2g second" || len(published) != 1 || published[0] != "synthetic-a" {
		t.Fatalf("Latest() = %q, %v, %v", entry.Command, published, found)
	}
	if err := MarkUndone(path, entry.At); err != nil {
		t.Fatal(err)
	}
	if entry, _, found := Load(path).Latest(start.Add(time.Hour)); !found || entry.Command != "g2g first" {
		t.Errorf("after undoing the second, Latest() = %q, %v", entry.Command, found)
	}
	if _, _, found := Load(path).Latest(start.Add(expiry + time.Hour)); found {
		t.Error("an operation too old to trust was offered")
	}

	// An entry too old to trust is dropped when the next one is written.
	late := start.Add(expiry + time.Hour)
	if err := Append(path, Entry{At: late, Command: "g2g late", Refs: move}, late); err != nil {
		t.Fatal(err)
	}
	if entries := Load(path).Entries; len(entries) != 1 || entries[0].Command != "g2g late" {
		t.Errorf("entries after expiry = %+v", entries)
	}
	if _, _, found := Load(filepath.Join(t.TempDir(), "synthetic-missing.json")).Latest(start); found {
		t.Error("a missing log offered an operation")
	}
}

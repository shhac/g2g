package undo

import (
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/oplog"
)

func entry() oplog.Entry {
	edge := graph.Edge{Parent: "synthetic-main", ForkPoint: "main-2"}
	was := graph.Edge{Parent: "synthetic-main", ForkPoint: "main-1"}
	return oplog.Entry{
		Command: "g2g synthetic",
		Refs: map[string]oplog.Move{
			"refs/heads/synthetic-a":          {Before: "a-1", After: "a-2"},
			"refs/g2g/forkpoints/synthetic-a": {Before: "main-1", After: "main-2"},
		},
		Edges:    map[string]oplog.Recorded{"synthetic-a": {Before: &was, After: &edge}},
		Tips:     map[string]string{"synthetic-a": "a-2"},
		Children: map[string][]string{"synthetic-a": {}},
	}
}

func left() oplog.Reading {
	return oplog.Reading{
		Refs:  map[string]string{"refs/heads/synthetic-a": "a-2", "refs/g2g/forkpoints/synthetic-a": "main-2", "refs/heads/synthetic-other": "other-1"},
		Graph: graph.Graph{Trunks: []string{"synthetic-main"}, Edges: map[string]graph.Edge{"synthetic-a": {Parent: "synthetic-main", ForkPoint: "main-2"}}},
	}
}

// Undo never undoes work done since: whatever the operation changed that has
// changed again is named. What it left alone may move freely, and what an
// undo that stopped part-way has already put back does not refuse the rest.
func TestSinceNamesWhatHasChangedAgain(t *testing.T) {
	changes := map[string]struct {
		change func(*oplog.Reading)
		want   string
	}{
		"nothing":                    {func(*oplog.Reading) {}, ""},
		"an unrelated branch moved":  {func(now *oplog.Reading) { now.Refs["refs/heads/synthetic-other"] = "other-2" }, ""},
		"the branch gained a commit": {func(now *oplog.Reading) { now.Refs["refs/heads/synthetic-a"] = "a-3" }, "synthetic-a has moved since, from a-2 to a-3"},
		"the branch was deleted":     {func(now *oplog.Reading) { delete(now.Refs, "refs/heads/synthetic-a") }, "synthetic-a has been deleted since"},
		"its pin moved":              {func(now *oplog.Reading) { now.Refs["refs/g2g/forkpoints/synthetic-a"] = "main-3" }, "refs/g2g/forkpoints/synthetic-a has moved since"},
		"its record changed":         {func(now *oplog.Reading) { now.Graph.Edges["synthetic-a"] = graph.Edge{Parent: "synthetic-other"} }, "what the graph records of synthetic-a has changed since"},
		"a child was recorded on it": {func(now *oplog.Reading) { now.Graph.Edges["synthetic-b"] = graph.Edge{Parent: "synthetic-a"} }, "the branches recorded on synthetic-a have changed since"},
		"the branch is already back": {func(now *oplog.Reading) { now.Refs["refs/heads/synthetic-a"] = "a-1" }, ""},
		"the record is already back": {func(now *oplog.Reading) {
			now.Graph.Edges["synthetic-a"] = graph.Edge{Parent: "synthetic-main", ForkPoint: "main-1"}
		}, ""},
		"the trunks changed unrecorded": {func(now *oplog.Reading) { now.Graph.Trunks = []string{"synthetic-main", "synthetic-other"} }, ""},
	}
	for name, each := range changes {
		t.Run(name, func(t *testing.T) {
			now := left()
			each.change(&now)
			if got := since(entry(), now); !strings.HasPrefix(got, each.want) || (each.want == "") != (got == "") {
				t.Errorf("since() = %q, want %q", got, each.want)
			}
		})
	}
}

// A branch whose record alone changed is still the branch it was about only
// while it is where the operation left it.
func TestSinceAsksAfterABranchWhoseRecordAloneChanged(t *testing.T) {
	recorded := entry()
	delete(recorded.Refs, "refs/heads/synthetic-a")
	now := left()
	if got := since(recorded, now); got != "" {
		t.Fatalf("since() = %q with the branch where it was", got)
	}
	now.Refs["refs/heads/synthetic-a"] = "a-rebased"
	if got := since(recorded, now); !strings.HasPrefix(got, "synthetic-a has moved since") {
		t.Errorf("since() = %q, want the rebased branch named", got)
	}
}

// A rename removes one name and restores another at the same commit, so the
// name it removes loses nothing. A branch created with a commit of its own
// restores nothing.
func TestRestoresTellsARenameFromABranchWithWorkOfItsOwn(t *testing.T) {
	renamed := oplog.Entry{Refs: map[string]oplog.Move{
		"refs/heads/synthetic-new": {After: "tip"},
		"refs/heads/synthetic-old": {Before: "tip"},
	}}
	if !restores(renamed, "refs/heads/synthetic-new") {
		t.Error("a rename is not seen as restoring the old name at the same commit")
	}
	created := oplog.Entry{Refs: map[string]oplog.Move{
		"refs/heads/synthetic-new":          {After: "tip"},
		"refs/g2g/forkpoints/synthetic-new": {After: "tip"},
		"refs/heads/synthetic-other":        {Before: "other", After: "moved"},
	}}
	if restores(created, "refs/heads/synthetic-new") {
		t.Error("a created branch is seen as restored elsewhere")
	}
}

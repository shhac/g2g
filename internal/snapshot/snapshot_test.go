package snapshot

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/shhac/g2g/internal/graph"
)

type ancestry map[[2]string]bool

func (a ancestry) IsAncestor(_ context.Context, ancestor, descendant string) (bool, error) {
	return a[[2]string{ancestor, descendant}], nil
}

func world() World {
	return World{
		Tips:    map[string]string{"synthetic-main": "main-1", "synthetic-a": "a-1", "synthetic-other": "other-1"},
		Graph:   graph.Graph{Trunks: []string{"synthetic-main"}, Edges: map[string]graph.Edge{"synthetic-a": {Parent: "synthetic-main", ForkPoint: "main-0"}, "synthetic-other": {Parent: "synthetic-main"}}},
		Holders: map[string]string{"synthetic-other": "synthetic/elsewhere"},
		Here:    "refs/heads/synthetic-a",
	}
}

// What a plan rests on is compared; the rest of the repository is free to
// move, which is what lets a snapshot be applied in one several sessions
// share.
func TestABasisNamesWhatChangedAndIgnoresTheRest(t *testing.T) {
	rests := Rests{Branches: []string{"synthetic-a"}, Advancing: []string{"synthetic-main"}, Here: true}
	changes := map[string]struct {
		change func(*World)
		want   string
	}{
		"nothing":                     {func(*World) {}, ""},
		"another branch moved":        {func(w *World) { w.Tips["synthetic-other"] = "other-2" }, ""},
		"another branch was recorded": {func(w *World) { w.Graph.Edges["synthetic-new"] = graph.Edge{Parent: "synthetic-other"} }, ""},
		"another branch was let go":   {func(w *World) { delete(w.Holders, "synthetic-other") }, ""},
		"the trunk advanced":          {func(w *World) { w.Tips["synthetic-main"] = "main-2" }, ""},
		"the trunk was rewritten":     {func(w *World) { w.Tips["synthetic-main"] = "main-rewritten" }, "synthetic-main was rewritten: it no longer contains main-1"},
		"the branch moved":            {func(w *World) { w.Tips["synthetic-a"] = "a-2" }, "synthetic-a moved from a-1 to a-2"},
		"the branch was deleted":      {func(w *World) { delete(w.Tips, "synthetic-a") }, "synthetic-a no longer exists"},
		"its record changed": {func(w *World) {
			w.Graph.Edges["synthetic-a"] = graph.Edge{Parent: "synthetic-main", ForkPoint: "main-1"}
		}, "what the graph records of synthetic-a changed"},
		"a child was recorded on it": {func(w *World) { w.Graph.Edges["synthetic-b"] = graph.Edge{Parent: "synthetic-a"} }, "the branches recorded on synthetic-a changed"},
		"a worktree took it":         {func(w *World) { w.Holders["synthetic-a"] = "synthetic/taken" }, "synthetic-a is checked out in synthetic/taken"},
		"the checkout moved":         {func(w *World) { w.Here = "refs/heads/synthetic-other" }, "this checkout moved from refs/heads/synthetic-a to refs/heads/synthetic-other"},
	}
	contains := ancestry{{"main-1", "main-2"}: true}
	for name, each := range changes {
		t.Run(name, func(t *testing.T) {
			basis := world().Basis(rests)
			now := world()
			each.change(&now)
			got, err := basis.Changed(context.Background(), contains, now)
			if err != nil || got != each.want {
				t.Errorf("Changed() = %q, %v, want %q", got, err, each.want)
			}
		})
	}
}

// A file is read back as it was written, and one that was not made for this
// run is refused before its plan is looked at.
func TestAFileAnswersOnlyWhatItWasMadeFor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synthetic.json")
	written := File{Tool: "v0.0.0-synthetic", Command: "g2g synthetic", Request: []string{"--branch=synthetic-a"}, Basis: world().Basis(Rests{Branches: []string{"synthetic-a"}}), Plan: []byte(`{"synthetic":true}`)}
	if err := Save(path, written); err != nil {
		t.Fatal(err)
	}
	read, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(read.Plan) == "" || read.Basis.Tips["synthetic-a"] != "a-1" {
		t.Fatalf("read back %+v", read)
	}
	if err := read.Answers("v0.0.0-synthetic", "g2g synthetic", []string{"--branch=synthetic-a"}); err != nil {
		t.Errorf("the request it was made for: %v", err)
	}
	for name, err := range map[string]error{
		"another version": read.Answers("v0.0.1-synthetic", "g2g synthetic", []string{"--branch=synthetic-a"}),
		"another command": read.Answers("v0.0.0-synthetic", "g2g other", []string{"--branch=synthetic-a"}),
		"another request": read.Answers("v0.0.0-synthetic", "g2g synthetic", []string{"--branch=synthetic-b"}),
	} {
		if err == nil {
			t.Errorf("%s was answered", name)
		}
	}
	if _, err := Load(filepath.Join(t.TempDir(), "synthetic-missing.json")); err == nil {
		t.Error("a missing snapshot was loaded")
	}
}

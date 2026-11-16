package restack

import (
	"reflect"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/worktree"
)

func TestLeavingOntoRefusesOnlyASelectionRoot(t *testing.T) {
	root := worktree.Holder{Branch: "synthetic-a", Path: "/synthetic-root"}
	middle := worktree.Holder{Branch: "synthetic-b", Path: "/synthetic-middle"}
	top := worktree.Holder{Branch: "synthetic-c", Path: "/synthetic-top"}
	for _, test := range []struct {
		name     string
		branches []string
		split    worktree.Split
		blocked  string
	}{
		{"top left", []string{root.Branch, middle.Branch, top.Branch}, worktree.Split{Leave: []worktree.Holder{top}}, ""},
		{"middle left", []string{root.Branch, middle.Branch, top.Branch}, worktree.Split{Leave: []worktree.Holder{middle}}, ""},
		{"several descendants left", []string{root.Branch, middle.Branch, top.Branch}, worktree.Split{Leave: []worktree.Holder{middle, top}}, ""},
		{"root left", []string{root.Branch, middle.Branch, top.Branch}, worktree.Split{Leave: []worktree.Holder{root}}, "has to move synthetic-a"},
		{"subtree root left", []string{middle.Branch, top.Branch}, worktree.Split{Leave: []worktree.Holder{middle}}, "has to move synthetic-b"},
		{"root after descendant", []string{root.Branch, middle.Branch, top.Branch}, worktree.Split{Leave: []worktree.Holder{top, root}}, "has to move synthetic-a"},
		{"refuse policy still refuses descendant", []string{root.Branch, middle.Branch, top.Branch}, worktree.Split{Refuse: []worktree.Holder{top}}, "checked out in another worktree"},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := Plan{
				Discovery: graph.Discovery{Graph: ontoStack(), Branches: test.branches},
				Onto:      ToBranch("synthetic-release"),
			}
			left, note := leaving(plan, test.split)
			if test.blocked != "" {
				if !strings.Contains(note.Reason, test.blocked) || len(note.Ways) == 0 || len(left) != 0 {
					t.Fatalf("leaving = %v, %+v, want refusal naming %q and ways out", left, note, test.blocked)
				}
				return
			}
			if note.Reason != "" || !reflect.DeepEqual(left, test.split.Leave) {
				t.Errorf("leaving = %v, %+v, want descendants left unchanged", left, note)
			}
		})
	}
}

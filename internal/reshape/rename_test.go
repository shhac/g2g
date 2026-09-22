package reshape

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/repair"
)

func TestPlanRenameDecidesWhetherABranchCanBeRenamed(t *testing.T) {
	for _, test := range []struct {
		name    string
		from    string
		to      string
		arrange func(world)
		blocked string
		way     string
	}{
		{name: "the branch checked out", to: "synthetic-renamed"},
		{name: "a trunk", from: "synthetic-main", to: "synthetic-trunk"},
		{
			// git branch -m moves that worktree's HEAD with the branch, so it
			// is reported rather than refused.
			name: "a branch another worktree has", from: "synthetic-top", to: "synthetic-renamed",
			arrange: func(w world) { w.git.elsewhere = map[string]string{"synthetic-top": "/synthetic/elsewhere"} },
		},
		{name: "an option-like name", to: "-synthetic", blocked: "cannot be passed safely"},
		{
			name: "a name git refuses", to: "synthetic..bad",
			arrange: func(w world) { w.git.invalid = map[string]bool{"synthetic..bad": true} },
			blocked: "synthetic invalid name",
		},
		{name: "the same name", to: "synthetic-middle", blocked: "already has that name"},
		{name: "a name another branch has", to: "synthetic-side", blocked: "already exists"},
		{
			name: "a name the graph still records from a deleted branch", to: "synthetic-deleted",
			arrange: func(w world) {
				w.store.adopted.Edges["synthetic-deleted"] = w.store.adopted.Edges["synthetic-side"]
			},
			blocked: "already records synthetic-deleted",
		},
		{
			name: "a branch the graph does not record", from: "synthetic-stray", to: "synthetic-renamed",
			arrange: func(w world) { w.git.local = append(w.git.local, "synthetic-stray") },
			blocked: "does not record synthetic-stray", way: "g2g track --branch synthetic-stray",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := newWorld()
			if test.arrange != nil {
				test.arrange(w)
			}

			plan, err := w.service().PlanRename(context.Background(), test.from, test.to)
			if err != nil {
				t.Fatalf("PlanRename() error = %v", err)
			}

			if test.blocked == "" {
				if plan.Blocked != "" {
					t.Fatalf("Blocked = %q, want the plan to proceed", plan.Blocked)
				}
				return
			}
			if !strings.Contains(plan.Blocked, test.blocked) {
				t.Errorf("Blocked = %q, want it to contain %q", plan.Blocked, test.blocked)
			}
			if plan.Blocked != plan.Repair.Sentence() {
				t.Errorf("Blocked %q is not the repair's sentence", plan.Blocked)
			}
			if test.way != "" && !slices.ContainsFunc(plan.Repair.Ways, func(way repair.Step) bool { return way.Command == test.way }) {
				t.Errorf("repair offers %+v, want %q", plan.Repair.Ways, test.way)
			}
		})
	}
}

// check-ref-format would read an option-like name as one of its own flags, so
// it must not be asked.
func TestAnOptionLikeNameNeverReachesGit(t *testing.T) {
	w := newWorld()
	if _, err := w.service().PlanRename(context.Background(), "", "--synthetic"); err != nil {
		t.Fatal(err)
	}
	if len(w.git.calls) != 0 {
		t.Errorf("git was asked %v about an option-like name", w.git.calls)
	}
}

func TestRenamePlanSaysWhatStaysUnderTheOldName(t *testing.T) {
	w := newWorld()
	w.git.remote = map[string][]string{"synthetic-middle": {"origin/synthetic-middle"}}

	plan, err := w.service().PlanRename(context.Background(), "synthetic-middle", "synthetic-renamed")
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(plan.Remote, []string{"origin/synthetic-middle"}) {
		t.Errorf("Remote = %v", plan.Remote)
	}
	if !slices.Equal(plan.Children, []string{"synthetic-top"}) || plan.Updated.Edges["synthetic-top"].Parent != "synthetic-renamed" {
		t.Errorf("children %v are not moved to the new name: %+v", plan.Children, plan.Updated.Edges)
	}
}

func TestApplyRenameMovesTheBranchTheRecordAndThePin(t *testing.T) {
	w := newWorld()
	service := w.service()
	plan, err := service.PlanRename(context.Background(), "synthetic-middle", "synthetic-renamed")
	if err != nil {
		t.Fatal(err)
	}

	if err := service.ApplyRename(context.Background(), plan); err != nil {
		t.Fatalf("ApplyRename() = %v", err)
	}

	want := []string{"branch -m synthetic-middle synthetic-renamed", "save", "pin synthetic-renamed lower-tip", "unpin synthetic-middle"}
	if got := w.mutations(); !slices.Equal(got, want) {
		t.Errorf("calls = %v\nwant    %v", got, want)
	}
	if edge := w.store.adopted.Edges["synthetic-renamed"]; edge.Parent != "synthetic-lower" {
		t.Errorf("the store records the renamed branch as %+v", edge)
	}
}

// A trunk has no edge of its own and so no pin to move.
func TestRenamingATrunkMovesNoPin(t *testing.T) {
	w := newWorld()
	service := w.service()
	plan, err := service.PlanRename(context.Background(), "synthetic-main", "synthetic-trunk")
	if err != nil {
		t.Fatal(err)
	}

	if err := service.ApplyRename(context.Background(), plan); err != nil {
		t.Fatalf("ApplyRename() = %v", err)
	}
	if got := w.mutations(); !slices.Equal(got, []string{"branch -m synthetic-main synthetic-trunk", "save"}) {
		t.Errorf("calls = %v", got)
	}
	if !slices.Equal(w.store.adopted.Trunks, []string{"synthetic-trunk"}) {
		t.Errorf("trunks = %v", w.store.adopted.Trunks)
	}
}

// The branch and its record must never disagree about its name, so a record
// or pin that cannot follow puts the branch's name back.
func TestAFailedRenameIsPutBack(t *testing.T) {
	for _, test := range []struct {
		name    string
		arrange func(world)
		undone  []string
	}{
		{
			name:    "the record cannot be written",
			arrange: func(w world) { w.store.failAt = 1 },
			undone:  []string{"save", "branch -m synthetic-renamed synthetic-middle"},
		},
		{
			name: "the pin cannot be moved",
			arrange: func(w world) {
				w.pins.fail = map[string]error{"pin synthetic-renamed": errors.New("synthetic ref failure")}
			},
			undone: []string{"pin synthetic-renamed lower-tip", "save", "branch -m synthetic-renamed synthetic-middle"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := newWorld()
			test.arrange(w)
			before := w.store.adopted.Clone()
			service := w.service()
			plan, err := service.PlanRename(context.Background(), "synthetic-middle", "synthetic-renamed")
			if err != nil {
				t.Fatal(err)
			}

			err = service.ApplyRename(context.Background(), plan)

			var rolledBack *RolledBack
			if !errors.As(err, &rolledBack) || rolledBack.Stuck() {
				t.Fatalf("ApplyRename() = %v, want a complete rollback", err)
			}
			calls := w.mutations()
			if tail := calls[len(calls)-len(test.undone):]; !slices.Equal(tail, test.undone) {
				t.Errorf("calls = %v, want them to end %v", calls, test.undone)
			}
			if !w.store.adopted.Equal(before) {
				t.Errorf("the store was left as %+v", w.store.adopted.Edges)
			}
		})
	}
}

func TestAnOldPinThatCannotBeReleasedLeavesTheRenameDone(t *testing.T) {
	w := newWorld()
	w.pins.fail = map[string]error{"unpin synthetic-middle": errors.New("synthetic ref failure")}
	service := w.service()
	plan, err := service.PlanRename(context.Background(), "synthetic-middle", "synthetic-renamed")
	if err != nil {
		t.Fatal(err)
	}

	var partial *Partial
	if err := service.ApplyRename(context.Background(), plan); !errors.As(err, &partial) {
		t.Fatalf("ApplyRename() = %v, want a part-way result", err)
	}
	if !w.store.adopted.Tracked("synthetic-renamed") {
		t.Error("the rename was undone")
	}
}

func TestRevalidationRefusesANameTakenSincePreview(t *testing.T) {
	w := newWorld()
	service := w.service()
	preview, err := service.PlanRename(context.Background(), "synthetic-middle", "synthetic-renamed")
	if err != nil {
		t.Fatal(err)
	}
	w.git.local = append(w.git.local, "synthetic-renamed")

	if _, err := service.RevalidateRename(context.Background(), "synthetic-middle", "synthetic-renamed", preview); err == nil {
		t.Fatal("RevalidateRename() = nil after the name was taken")
	}
}

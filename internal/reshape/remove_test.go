package reshape

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/repair"
)

func TestPlanDecidesWhetherABranchCanBeRemoved(t *testing.T) {
	for _, test := range []struct {
		name      string
		operation Operation
		branch    string
		arrange   func(*fakeGit)
		// blocked is a fragment of the refusal, empty when the plan proceeds.
		blocked string
		// way is a command the refusal must offer.
		way string
	}{
		{name: "delete a branch in the middle of a stack", operation: Delete, branch: "synthetic-middle"},
		{name: "delete the branch checked out", operation: Delete},
		{name: "fold a branch sitting on its parent", operation: Fold, branch: "synthetic-middle"},
		{
			name: "delete a branch the graph does not record", operation: Delete, branch: "synthetic-stray",
			arrange: func(g *fakeGit) { g.local = append(g.local, "synthetic-stray") },
			blocked: "does not record synthetic-stray", way: "g2g track --branch synthetic-stray",
		},
		{name: "delete a trunk", operation: Delete, branch: "synthetic-main", blocked: "is a trunk"},
		{
			name: "delete a recorded branch that is already gone", operation: Delete, branch: "synthetic-top",
			arrange: func(g *fakeGit) {
				g.local = slices.DeleteFunc(g.local, func(b string) bool { return b == "synthetic-top" })
			},
			blocked: "not a local branch", way: "g2g untrack --branch synthetic-top",
		},
		{
			name: "delete a branch whose parent is gone", operation: Delete, branch: "synthetic-top",
			arrange: func(g *fakeGit) {
				g.local = slices.DeleteFunc(g.local, func(b string) bool { return b == "synthetic-middle" })
			},
			blocked: "nowhere here to put",
		},
		{
			// git would refuse the delete itself, after the record was written.
			name: "delete a branch another worktree has", operation: Delete, branch: "synthetic-top",
			arrange: func(g *fakeGit) { g.elsewhere = map[string]string{"synthetic-top": "/synthetic/elsewhere"} },
			blocked: "checked out in another worktree: synthetic-top (/synthetic/elsewhere)",
		},
		{
			name: "fold into a trunk", operation: Fold, branch: "synthetic-lower",
			blocked: "would move the trunk", way: "g2g land --branch synthetic-lower",
		},
		{
			// Checked before the worktree: a trunk is the reason that holds
			// whatever else is true, and the primary worktree is usually on it.
			name: "fold into a trunk checked out elsewhere", operation: Fold, branch: "synthetic-lower",
			arrange: func(g *fakeGit) { g.elsewhere = map[string]string{"synthetic-main": "/synthetic/primary"} },
			blocked: "would move the trunk",
		},
		{
			name: "fold onto a parent that moved on", operation: Fold, branch: "synthetic-top",
			blocked: "cannot be fast-forwarded", way: "g2g restack --branch synthetic-top",
		},
		{
			name: "fold into a parent another worktree has", operation: Fold, branch: "synthetic-middle",
			arrange: func(g *fakeGit) { g.elsewhere = map[string]string{"synthetic-lower": "/synthetic/elsewhere"} },
			blocked: "checked out in another worktree: synthetic-lower",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := newWorld()
			if test.arrange != nil {
				test.arrange(w.git)
			}

			plan, err := w.service().Plan(context.Background(), test.operation, test.branch)
			if err != nil {
				t.Fatalf("Plan() error = %v", err)
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
				t.Errorf("Blocked %q is not the repair's sentence %q", plan.Blocked, plan.Repair.Sentence())
			}
			if test.way != "" && !slices.ContainsFunc(plan.Repair.Ways, func(way repair.Step) bool { return way.Command == test.way }) {
				t.Errorf("repair offers %+v, want %q among them", plan.Repair.Ways, test.way)
			}
		})
	}
}

// The user asked for the branch to go, so what sat on it goes on what it sat
// on — stated, not guessed — and each child keeps the fork point that keeps
// the deleted branch's commits out of its next replay.
func TestDeletingABranchRecordsItsChildrenOnItsParent(t *testing.T) {
	w := newWorld()

	plan, err := w.service().Plan(context.Background(), Delete, "synthetic-middle")
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(plan.Children, []string{"synthetic-top"}) {
		t.Errorf("Children = %v", plan.Children)
	}
	edge := plan.Updated.Edges["synthetic-top"]
	if edge.Parent != "synthetic-lower" || edge.ForkPoint != "middle-tip" {
		t.Errorf("synthetic-top will be recorded as %+v, want under synthetic-lower forking at middle-tip", edge)
	}
	if plan.Updated.Tracked("synthetic-middle") {
		t.Error("the deleted branch is still recorded")
	}
}

// What a delete loses is what nothing else holds: a commit whose content is
// in the parent, or one a remote-tracking ref reaches, survives it.
func TestDeleteNamesTheCommitsThatExistNowhereElse(t *testing.T) {
	w := newWorld()

	plan, err := w.service().Plan(context.Background(), Delete, "synthetic-middle")
	if err != nil {
		t.Fatal(err)
	}

	want := []git.Commit{{ID: "middle-own", Subject: "synthetic middle work"}}
	if !slices.Equal(plan.Unique, want) {
		t.Errorf("Unique = %+v, want %+v", plan.Unique, want)
	}
}

// A squash merge puts every commit's content in the parent under one commit
// that matches none of them, so asking per commit alone would list work that
// has already landed as about to be lost.
func TestDeletingASquashedBranchLosesNothing(t *testing.T) {
	w := newWorld()
	w.git.absorbed = true

	plan, err := w.service().Plan(context.Background(), Delete, "synthetic-middle")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Unique) != 0 {
		t.Errorf("Unique = %+v, want nothing for a branch whose work has landed", plan.Unique)
	}
}

// Folding moves the parent, so every other branch on it will need a restack.
func TestFoldNamesTheSiblingsItLeavesBehind(t *testing.T) {
	w := newWorld()

	plan, err := w.service().Plan(context.Background(), Fold, "synthetic-middle")
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(plan.Siblings, []string{"synthetic-side"}) {
		t.Errorf("Siblings = %v", plan.Siblings)
	}
	if !plan.Moves() || len(plan.Unique) != 0 {
		t.Errorf("a fold moves its parent and loses nothing: %+v", plan)
	}
}

func TestApplyRunsEachRemovalInItsOrder(t *testing.T) {
	for _, test := range []struct {
		name      string
		operation Operation
		branch    string
		current   string
		want      []string
	}{
		{
			// Switching away comes first: git will not delete the branch it is on.
			name: "delete the branch checked out", operation: Delete, branch: "synthetic-middle", current: "synthetic-middle",
			want: []string{"switch synthetic-lower", "save", "branch -D synthetic-middle", "unpin synthetic-middle"},
		},
		{
			name: "delete a branch elsewhere in the stack", operation: Delete, branch: "synthetic-middle", current: "synthetic-top",
			want: []string{"save", "branch -D synthetic-middle", "unpin synthetic-middle"},
		},
		{
			// The parent is where the checkout is, so the tree follows the ref.
			name: "fold into the branch checked out", operation: Fold, branch: "synthetic-middle", current: "synthetic-lower",
			want: []string{"update-ref synthetic-lower middle-tip lower-tip", "read-tree lower-tip middle-tip", "save", "branch -D synthetic-middle", "unpin synthetic-middle"},
		},
		{
			name: "fold the branch checked out", operation: Fold, branch: "synthetic-middle", current: "synthetic-middle",
			want: []string{"update-ref synthetic-lower middle-tip lower-tip", "switch synthetic-lower", "save", "branch -D synthetic-middle", "unpin synthetic-middle"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := newWorld()
			w.git.current = test.current
			service := w.service()
			plan, err := service.Plan(context.Background(), test.operation, test.branch)
			if err != nil {
				t.Fatal(err)
			}

			if err := service.Apply(context.Background(), plan); err != nil {
				t.Fatalf("Apply() = %v", err)
			}

			if got := w.mutations(); !slices.Equal(got, test.want) {
				t.Errorf("calls = %v\nwant    %v", got, test.want)
			}
			if w.store.adopted.Tracked("synthetic-middle") || w.store.adopted.Edges["synthetic-top"].Parent != "synthetic-lower" {
				t.Errorf("the store records %+v", w.store.adopted.Edges)
			}
		})
	}
}

// Everything up to deleting the branch can be put back, and is: the record,
// the checkout, and a parent a fold had moved.
func TestAFailedRemovalIsPutBack(t *testing.T) {
	for _, test := range []struct {
		name      string
		operation Operation
		current   string
		arrange   func(world)
		// undone is the tail of the call list the rollback must produce.
		undone []string
	}{
		{
			name: "the branch cannot be deleted", operation: Delete, current: "synthetic-middle",
			arrange: func(w world) {
				w.git.fail = map[string]error{"branch -D synthetic-middle": errors.New("synthetic refusal")}
			},
			undone: []string{"save", "switch synthetic-middle"},
		},
		{
			name: "the record cannot be written", operation: Fold, current: "synthetic-lower",
			arrange: func(w world) { w.store.failAt = 1 },
			undone:  []string{"read-tree middle-tip lower-tip", "update-ref synthetic-lower lower-tip middle-tip"},
		},
		{
			// read-tree refuses rather than overwrite a local change, and the
			// ref it was following goes back where it was.
			name: "the checkout cannot follow the parent", operation: Fold, current: "synthetic-lower",
			arrange: func(w world) {
				w.git.fail = map[string]error{"read-tree lower-tip middle-tip": errors.New("synthetic local change")}
			},
			undone: []string{"update-ref synthetic-lower lower-tip middle-tip"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := newWorld()
			w.git.current = test.current
			test.arrange(w)
			before := w.store.adopted.Clone()
			service := w.service()
			plan, err := service.Plan(context.Background(), test.operation, "synthetic-middle")
			if err != nil {
				t.Fatal(err)
			}

			err = service.Apply(context.Background(), plan)

			var rolledBack *RolledBack
			if !errors.As(err, &rolledBack) || rolledBack.Stuck() {
				t.Fatalf("Apply() = %v, want a complete rollback", err)
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

// A switch that fails is git refusing to overwrite a local change, and it
// comes before anything else, so there is nothing to put back.
func TestADeleteWhoseSwitchIsRefusedChangesNothing(t *testing.T) {
	w := newWorld()
	w.git.fail = map[string]error{"switch synthetic-lower": errors.New("synthetic local change")}
	service := w.service()
	plan, err := service.Plan(context.Background(), Delete, "synthetic-middle")
	if err != nil {
		t.Fatal(err)
	}

	err = service.Apply(context.Background(), plan)

	var rolledBack *RolledBack
	if err == nil || errors.As(err, &rolledBack) {
		t.Fatalf("Apply() = %v, want the plain refusal", err)
	}
	if got := w.mutations(); !slices.Equal(got, []string{"switch synthetic-lower"}) {
		t.Errorf("calls = %v", got)
	}
}

func TestARollbackThatCannotFinishSaysSo(t *testing.T) {
	w := newWorld()
	w.git.fail = map[string]error{
		"branch -D synthetic-middle": errors.New("synthetic refusal"),
		"switch synthetic-middle":    errors.New("synthetic switch failure"),
	}
	service := w.service()
	plan, err := service.Plan(context.Background(), Delete, "synthetic-middle")
	if err != nil {
		t.Fatal(err)
	}

	err = service.Apply(context.Background(), plan)

	var rolledBack *RolledBack
	if !errors.As(err, &rolledBack) || !rolledBack.Stuck() {
		t.Fatalf("Apply() = %v, want a rollback that says it did not finish", err)
	}
}

// Releasing the pin is tidying: the branch is gone and its children recorded,
// and undoing that because a ref could not be removed would be worse.
func TestAPinThatCannotBeReleasedLeavesTheRemovalDone(t *testing.T) {
	w := newWorld()
	w.pins.fail = map[string]error{"unpin synthetic-middle": errors.New("synthetic ref failure")}
	service := w.service()
	plan, err := service.Plan(context.Background(), Delete, "synthetic-middle")
	if err != nil {
		t.Fatal(err)
	}

	err = service.Apply(context.Background(), plan)

	var partial *Partial
	if !errors.As(err, &partial) {
		t.Fatalf("Apply() = %v, want a part-way result", err)
	}
	if w.store.adopted.Tracked("synthetic-middle") {
		t.Error("the removal was undone")
	}
}

func TestABlockedRemovalIsNeverApplied(t *testing.T) {
	w := newWorld()
	service := w.service()
	plan, err := service.Plan(context.Background(), Fold, "synthetic-lower")
	if err != nil {
		t.Fatal(err)
	}

	if err := service.Apply(context.Background(), plan); err == nil {
		t.Fatal("Apply() of a blocked plan = nil")
	}
	if got := w.mutations(); len(got) != 0 {
		t.Errorf("a blocked plan ran %v", got)
	}
}

func TestRevalidationRefusesABranchThatMoved(t *testing.T) {
	w := newWorld()
	service := w.service()
	preview, err := service.Plan(context.Background(), Fold, "synthetic-middle")
	if err != nil {
		t.Fatal(err)
	}
	w.git.tips["synthetic-middle"] = "moved-tip"

	if _, err := service.Revalidate(context.Background(), Fold, "synthetic-middle", preview); err == nil {
		t.Fatal("Revalidate() = nil after the branch moved")
	}
}

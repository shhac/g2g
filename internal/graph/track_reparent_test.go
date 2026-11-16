package graph

import (
	"context"
	"testing"

	"github.com/shhac/g2g/internal/repair"
)

func TestPlanTrackReparentRequiresAnEvidencedOwnRange(t *testing.T) {
	for _, tc := range []struct {
		name          string
		fork          string
		intact        bool
		missing       bool
		sharedLater   bool
		builtOnParent bool
		want          string
	}{
		{name: "older sibling keeps the recorded range", fork: "synthetic-old-fork", intact: true, want: "synthetic-old-fork"},
		{name: "shared ancestry narrows the recorded range", fork: "synthetic-old-fork", intact: true, sharedLater: true, want: "synthetic-fork"},
		{name: "rewritten branch refuses a sibling", fork: "synthetic-old-fork"},
		{name: "missing boundary refuses a sibling", fork: "synthetic-old-fork", missing: true},
		{name: "legacy record refuses a sibling"},
		{name: "verified parent repairs a rewritten branch", fork: "synthetic-old-fork", builtOnParent: true, want: "synthetic-parent-tip"},
		{name: "verified parent repairs a missing boundary", fork: "synthetic-old-fork", missing: true, builtOnParent: true, want: "synthetic-parent-tip"},
		{name: "verified parent repairs a legacy record", builtOnParent: true, want: "synthetic-parent-tip"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			git := fakeAncestry{
				local:        []string{"synthetic-trunk", "synthetic-parent", "synthetic-target"},
				objects:      map[string]string{"synthetic-parent": "synthetic-parent-tip"},
				ancestors:    map[string][]string{"synthetic-target": {"synthetic-fork"}},
				unresolvable: map[string]bool{tc.fork: tc.missing},
			}
			if tc.intact {
				git.ancestors["synthetic-target"] = append(git.ancestors["synthetic-target"], tc.fork)
			}
			if tc.sharedLater {
				git.ancestors["synthetic-fork"] = []string{tc.fork}
			}
			if tc.builtOnParent {
				git.ancestors["synthetic-target"] = append(git.ancestors["synthetic-target"], "synthetic-parent", "synthetic-parent-tip")
			}
			recorded := Graph{Trunks: []string{"synthetic-trunk"}, Edges: map[string]Edge{
				"synthetic-parent": {Parent: "synthetic-trunk", ForkPoint: "synthetic-fork"},
				"synthetic-target": {Parent: "synthetic-trunk", ForkPoint: tc.fork},
			}}
			service, store := newService(t, git, recorded)
			ctx := context.Background()
			plan, err := service.PlanTrack(ctx, Selection{Branch: "synthetic-target"}, "synthetic-parent")
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if plan.Blocked() == "" || !plan.Updated.Equal(recorded) {
					t.Fatalf("ambiguous range was accepted: %+v", plan)
				}
				wantCommand := repair.Command([]string{"g2g", "track", "--branch", "synthetic-target"})
				if len(plan.Repair.Ways) != 1 || plan.Repair.Ways[0].Command != wantCommand {
					t.Errorf("repair = %+v, want %s", plan.Repair, wantCommand)
				}
				if err := service.ApplyTrack(ctx, plan); err == nil {
					t.Error("apply accepted an ambiguous range")
				}
			} else {
				edge := plan.Updated.Edges[plan.Target]
				if plan.Blocked() != "" || edge.ForkPoint != tc.want || edge.Parent != "synthetic-parent" {
					t.Fatalf("edge = %+v, blocked = %s; want fork %s", edge, plan.Blocked(), tc.want)
				}
				if intact, err := git.IsAncestor(ctx, edge.ForkPoint, plan.Target); err != nil || !intact {
					t.Errorf("new boundary is not in the branch: %v", err)
				}
			}
			if len(store.writes) != 0 {
				t.Fatal("preview or refusal wrote the graph")
			}
		})
	}
}

func TestRecordedBoundaryVerification(t *testing.T) {
	for _, tc := range []struct {
		name, boundary  string
		missing, intact bool
		want            string
		valid           bool
	}{
		{name: "legacy empty"},
		{name: "missing object", boundary: "synthetic-pin", missing: true},
		{name: "resolved but unrelated", boundary: "synthetic-pin", want: "synthetic-fork"},
		{name: "resolved ancestor", boundary: "synthetic-pin", intact: true, want: "synthetic-fork", valid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			git := fakeAncestry{objects: map[string]string{"synthetic-pin": "synthetic-fork"}, unresolvable: map[string]bool{"synthetic-pin": tc.missing}, ancestors: map[string][]string{}}
			if tc.intact {
				git.ancestors["synthetic-target"] = []string{"synthetic-fork"}
			}
			fork, valid, err := (Service{Git: git}).recordedBoundary(context.Background(), "synthetic-target", tc.boundary)
			if err != nil || fork != tc.want || valid != tc.valid {
				t.Fatalf("boundary=(%q,%t,%v); want (%q,%t,nil)", fork, valid, err, tc.want, tc.valid)
			}
		})
	}
}

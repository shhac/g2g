package submit

import (
	"context"
	"reflect"
	"testing"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/landed"
	"github.com/shhac/g2g/internal/stack"
)

func TestAbsorptionFiltersActionsWithoutChangingTopology(t *testing.T) {
	snapshot := stack.Snapshot{Source: stack.SourceG2G, Branches: []string{"synthetic-parent", "synthetic-child", "synthetic-top"}, Parents: map[string]string{"synthetic-parent": "synthetic-main", "synthetic-child": "synthetic-parent", "synthetic-top": "synthetic-child"}}
	plan := Plan{Snapshot: snapshot, Absorbed: map[string]landed.ParentAbsorption{"synthetic-child": {Local: landed.Present}}}
	if !reflect.DeepEqual(plan.Publication().Branches, []string{"synthetic-parent", "synthetic-top"}) || !reflect.DeepEqual(plan.Publication().Parents, snapshot.Parents) || !reflect.DeepEqual(plan.Snapshot, snapshot) {
		t.Fatal("eligibility changed topology")
	}
	if plan.Refusal(false).Reason == "" {
		t.Fatal("creation accepted an omitted base")
	}
	plan.Existing = []githubstack.PullRequest{{Head: "synthetic-top", Base: "synthetic-child", State: "OPEN", Number: 43}}
	if plan.absorbedBaseRepair(false).Reason != "" {
		t.Fatal("ordinary publication cannot preserve an existing PR")
	}
	if plan.LinkRepair().Reason == "" {
		t.Fatal("linking accepted a gap")
	}
}

// Apply must also protect callers that supply a plan directly. Its nonempty
// selection is distinct from the publication actions left after absorption.
func TestSubmitApplyWithEmptyPublicationSkipsAllActions(t *testing.T) {
	for _, linking := range []bool{false, true} {
		name := "without linking"
		if linking {
			name = "with linking"
		}
		t.Run(name, func(t *testing.T) {
			snapshot := stack.Snapshot{Source: stack.SourceG2G, Branches: []string{"synthetic-child", "synthetic-top"}, Parents: map[string]string{"synthetic-child": "synthetic-parent", "synthetic-top": "synthetic-child"}}
			plan := Plan{Snapshot: snapshot, Absorbed: map[string]landed.ParentAbsorption{"synthetic-child": {Local: landed.Present}, "synthetic-top": {Local: landed.Present}}}
			if len(plan.Publication().Branches) != 0 {
				t.Fatal("test requires an empty publication from a nonempty selection")
			}
			git, github := &fakeGit{}, &fakeGitHub{}
			if err := (Service{Git: git, GitHub: github, Pusher: git}).Apply(context.Background(), plan, NewSpec(nil, ""), linking); err != nil {
				t.Fatal(err)
			}
			if git.pushes != 0 || len(github.created) != 0 || github.links != 0 {
				t.Fatalf("actions: push=%d create=%v link=%d", git.pushes, github.created, github.links)
			}
		})
	}
}

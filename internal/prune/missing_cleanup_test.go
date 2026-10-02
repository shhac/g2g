package prune

import (
	"context"
	"testing"

	"github.com/shhac/g2g/internal/graph"
)

type missingAncestry struct {
	pruneAncestry
	local []string
}

func (a missingAncestry) LocalBranches(context.Context) ([]string, error) { return a.local, nil }

func TestForgetMissingRequiresEvidenceForASurvivingChild(t *testing.T) {
	for _, sits := range []bool{false, true} {
		t.Run(map[bool]string{false: "not replayed", true: "already replayed"}[sits], func(t *testing.T) {
			service, store, _, _ := syntheticService(t, "synthetic-c")
			service.Graph.Git = missingAncestry{pruneAncestry: pruneAncestry{current: "synthetic-c", sitting: map[string]bool{"synthetic-c": sits}}, local: []string{"synthetic-trunk", "synthetic-c"}}
			plan, err := service.PlanWithOptions(context.Background(), graph.Selection{Branch: "synthetic-c", Scope: graph.ScopeStack}, Options{ForgetMissing: true})
			if err != nil {
				t.Fatal(err)
			}
			if (plan.Blocked == "") != sits {
				t.Fatalf("unsafe missing cleanup: %+v", plan)
			}
			if !sits {
				if err := service.Apply(context.Background(), plan); err == nil || store.writes != 0 {
					t.Fatal("changed a refused graph")
				}
				return
			}
			if err := service.Apply(context.Background(), plan); err != nil {
				t.Fatal(err)
			}
			if parent, _ := store.graph.Parent("synthetic-c"); parent != "synthetic-trunk" {
				t.Fatalf("surviving child parent = %s", parent)
			}
			if store.graph.Tracked("synthetic-a") || store.graph.Tracked("synthetic-b") {
				t.Fatal("missing chain survived")
			}
		})
	}
}

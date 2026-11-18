package sync

import (
	"context"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/restack"
)

// stoppedElsewhere is a journal holding nothing here and one restack another
// worktree has stopped part-way.
type stoppedElsewhere struct{ record restack.Record }

func (stoppedElsewhere) Load(context.Context) (restack.Record, bool, error) {
	return restack.Record{}, false, nil
}
func (stoppedElsewhere) Start(context.Context, restack.Record) error { return nil }
func (stoppedElsewhere) Save(context.Context, restack.Record) error  { return nil }
func (stoppedElsewhere) Clear(context.Context) error                 { return nil }
func (j stoppedElsewhere) Elsewhere(context.Context) ([]restack.Elsewhere, error) {
	return []restack.Elsewhere{{Record: j.record, Checkout: "/synthetic/agent"}}, nil
}

// A pull is weighed against another worktree's stopped restack by what it
// does to each branch. Fast-forwarding the trunk both stacks sit on leaves
// what the restack has replayed standing; resetting that trunk to a rewritten
// one does not, and neither does taking, reforking or replaying a branch the
// restack is moving.
func TestAPullIsApartFromAStoppedRestackUnlessItMovesWhatThatReads(t *testing.T) {
	guard := restack.Service{Journal: stoppedElsewhere{restack.Record{
		Branch:    "synthetic-busy",
		Original:  map[string]string{"synthetic-busy": "1"},
		Structure: map[string]restack.RecordedEdge{"synthetic-busy": {Parent: "main"}},
	}}}
	replaying := func(branch string) restack.Plan {
		return restack.Plan{Discovery: graph.Discovery{
			Branches: []string{"main", branch},
			Graph:    graph.Graph{Edges: map[string]graph.Edge{branch: {Parent: "main"}}, Trunks: []string{"main"}},
		}}
	}
	for _, test := range []struct {
		name  string
		plan  Plan
		holds string
	}{
		{"fast-forwarding the shared trunk under another stack", Plan{Base: "main", Advance: true, Restack: replaying("synthetic-free")}, ""},
		{"the shared trunk already level", Plan{Base: "main", Restack: replaying("synthetic-free")}, ""},
		{"the trunk alone", Plan{Base: "main", Advance: true}, ""},
		{"resetting the shared trunk to a rewritten one", Plan{Base: "main", Supersede: true, Restack: replaying("synthetic-free")}, "main"},
		{"replaying the branch it is moving", Plan{Base: "main", Advance: true, Restack: replaying("synthetic-busy")}, "synthetic-busy"},
		{"taking the branch it is moving", Plan{Base: "main", Collect: []Collection{{Branch: "synthetic-busy"}}}, "synthetic-busy"},
		{"recording a new beginning for the branch it is moving", Plan{Base: "main", Begins: map[string]string{"synthetic-busy": "2"}}, "synthetic-busy"},
	} {
		err := guard.RequireApart(context.Background(), test.plan.Footprint())
		if test.holds == "" {
			if err != nil {
				t.Errorf("%s was refused: %v", test.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), "which holds "+test.holds) {
			t.Errorf("%s = %v, want a refusal over %s", test.name, err, test.holds)
		}
	}
}

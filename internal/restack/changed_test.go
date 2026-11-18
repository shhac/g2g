package restack

import (
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/graph"
)

// What an apply is refused over is named, because the ordinary cause is
// something else sharing the repository: a fetch, another session's restack,
// an edit to the record. Each of these read "restack plan changed" and no
// more.
func TestAPlanNamesWhatChangedSinceThePreview(t *testing.T) {
	oldTip, newTip := strings.Repeat("a", 40), strings.Repeat("b", 40)
	base := func() Plan {
		return Plan{
			Discovery: graph.Discovery{
				Target:   "synthetic-b",
				Branches: []string{"synthetic-a", "synthetic-b"},
				Graph: graph.Graph{Trunks: []string{"main"}, Edges: map[string]graph.Edge{
					"synthetic-a": {Parent: "main", ForkPoint: oldTip},
					"synthetic-b": {Parent: "synthetic-a", ForkPoint: oldTip},
				}},
				States: map[string]graph.NodeState{"synthetic-a": graph.StateNeedsRestack, "synthetic-b": graph.StateAligned},
			},
			Steps: []Step{{Branch: "synthetic-a", Parent: "main", Base: "main", Tip: oldTip, Head: oldTip, ForkPoint: oldTip}},
		}
	}
	changes := map[string]struct {
		change func(*Plan)
		want   string
	}{
		"the trunk moved":           {func(p *Plan) { p.Steps[0].Head = newTip }, "the commit synthetic-a is replayed onto moved from aaaaaaaaaaaa to bbbbbbbbbbbb"},
		"a branch gained a commit":  {func(p *Plan) { p.Steps[0].Tip = newTip }, "synthetic-a moved from aaaaaaaaaaaa to bbbbbbbbbbbb"},
		"another session reparents": {func(p *Plan) { p.Discovery.Graph.Edges["synthetic-b"] = graph.Edge{Parent: "main", ForkPoint: oldTip} }, "synthetic-b was recorded on main instead of synthetic-a"},
		"another session tracks":    {func(p *Plan) { p.Discovery.Graph.Edges["synthetic-c"] = graph.Edge{Parent: "main"} }, "synthetic-c was recorded on main"},
		"another session untracks":  {func(p *Plan) { delete(p.Discovery.Graph.Edges, "synthetic-b") }, "synthetic-b was forgotten from the record"},
		"a fork point is refreshed": {func(p *Plan) {
			p.Discovery.Graph.Edges["synthetic-b"] = graph.Edge{Parent: "synthetic-a", ForkPoint: newTip}
		}, "the record of where synthetic-b starts on synthetic-a changed"},
		"a branch's state changes": {func(p *Plan) { p.Discovery.States["synthetic-b"] = graph.StateNeedsRestack }, `synthetic-b went from "aligned" to "needs restack"`},
		"the tree becomes dirty":   {func(p *Plan) { p.Clean = true }, "the working tree gained or lost uncommitted changes"},
		"a step is added":          {func(p *Plan) { p.Steps = append(p.Steps, Step{Branch: "synthetic-b"}) }, "the branches to replay changed from 1 to 2"},
	}
	for name, each := range changes {
		t.Run(name, func(t *testing.T) {
			preview, current := base(), base()
			each.change(&current)
			if got := preview.Changed(current); got != each.want {
				t.Errorf("Changed() = %q, want %q", got, each.want)
			}
			if preview.Equal(current) {
				t.Error("a plan that names a change compares equal")
			}
		})
	}
	if preview := base(); preview.Changed(base()) != "" || !preview.Equal(base()) {
		t.Error("an unchanged plan names a change")
	}
}

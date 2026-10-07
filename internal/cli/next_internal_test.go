package cli

import (
	"testing"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/shape"
)

// A suggestion reaches what the command acted on: the branch when it was
// named, and the scope when the suggested command's default would not cover
// it. Otherwise it is the bare command, as the reader would type it.
func TestSuggestionsAimAtWhatWasSelected(t *testing.T) {
	for _, test := range []struct {
		name  string
		acted selected
		want  string
	}{
		{name: "standing on it", acted: selected{branch: "synthetic-a", scope: shape.ScopeSubtree}, want: "g2g push"},
		{name: "named", acted: selected{branch: "synthetic-a", named: true, scope: shape.ScopeStack}, want: "g2g push --branch synthetic-a"},
		{name: "narrower than the default", acted: selected{branch: "synthetic-a", named: true, scope: shape.ScopePath}, want: "g2g push --branch synthetic-a"},
		{name: "no scope", acted: selected{branch: "synthetic-a"}, want: "g2g push"},
		{name: "wider than push takes", acted: selected{branch: "synthetic-a", scope: shape.ScopeTrunk}, want: "g2g status --scope trunk"},
		{name: "wider, named", acted: selected{branch: "synthetic-a", named: true, scope: shape.ScopeTrunk}, want: "g2g status --branch synthetic-a --scope trunk"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.acted.next("g2g push", shape.ProjectScopes, shape.ScopeStack); got != test.want {
				t.Errorf("next = %q, want %q", got, test.want)
			}
		})
	}
	if got := (selected{branch: "synthetic-a", scope: shape.ScopeTrunk}).next("g2g prune", shape.ReadScopes, graph.ScopeStack); got != "g2g prune --scope trunk" {
		t.Errorf("prune over a trunk = %q, want it to name the scope it offers", got)
	}
}

package cli

import (
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/land"
)

// The flags are checked before anything is read, and a trunk's method is
// normalised the way land will read it back.
func TestTrunkFlagsAgreeBeforeAnythingIsRead(t *testing.T) {
	for name, tc := range map[string]struct {
		flags  trunkFlags
		parent string
		want   graph.Declaration
		fails  string
	}{
		"a trunk landing nowhere":     {flags: trunkFlags{asTrunk: true}},
		"a trunk landing somewhere":   {flags: trunkFlags{asTrunk: true, into: "synthetic-main", by: "rebase"}, want: graph.Declaration{Into: "synthetic-main", By: "rebase"}},
		"where and how without trunk": {flags: trunkFlags{into: "synthetic-main", by: "merge"}, fails: "add --as-trunk"},
		"a trunk with a parent":       {flags: trunkFlags{asTrunk: true}, parent: "synthetic-main", fails: "opposite answers"},
		"where without how":           {flags: trunkFlags{asTrunk: true, into: "synthetic-main"}, fails: "go together"},
		"how without where":           {flags: trunkFlags{asTrunk: true, by: "merge"}, fails: "go together"},
		"a method that is not one":    {flags: trunkFlags{asTrunk: true, into: "synthetic-main", by: "synthetic-nonsense"}, fails: "synthetic-nonsense"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := tc.flags.declaration(tc.parent)
			if tc.fails != "" {
				if err == nil || !strings.Contains(err.Error(), tc.fails) {
					t.Errorf("declaration() error = %v, want it to say %q", err, tc.fails)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Errorf("declaration() = %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
}

// Where the method came from is said, because it is the one descent whose
// method was not typed.
func TestTheLandPreviewSaysWhereTheMethodCameFrom(t *testing.T) {
	plan := land.Plan{Declaration: graph.Declaration{Into: "synthetic-main", By: "rebase"}}
	plan.Target = "synthetic-feature"

	plan.Options.Method = githubstack.MethodRebase
	if got := declaredMethodNote(plan); !strings.Contains(got, "by rebase, as declared") {
		t.Errorf("declaredMethodNote() = %q for the declared method", got)
	}
	plan.Options.Method = githubstack.MethodMerge
	if got := declaredMethodNote(plan); !strings.Contains(got, "by merge for this descent, not the declared rebase") {
		t.Errorf("declaredMethodNote() = %q for a method the run named", got)
	}
	if got := declaredMethodNote(land.Plan{}); got != "" {
		t.Errorf("declaredMethodNote() = %q for an ordinary descent", got)
	}
}

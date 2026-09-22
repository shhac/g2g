package align

import (
	"slices"
	"testing"

	"github.com/shhac/g2g/internal/graphite"
)

// A parsed display is another tool's claim and can contain a loop the g2g
// store never could. The order must still name every branch exactly once,
// parents first where there is a parent to be first.
func TestDeclaredOrderNamesEveryBranchOnceWhateverTheShape(t *testing.T) {
	forest := graphite.Forest{
		Parents: map[string]string{
			"synthetic-trunk": "",
			"synthetic-a":     "synthetic-trunk",
			"synthetic-a-one": "synthetic-a",
			"synthetic-b":     "synthetic-trunk",
			"synthetic-x":     "synthetic-y",
			"synthetic-y":     "synthetic-x",
			"synthetic-self":  "synthetic-self",
		},
		Roots: []string{"synthetic-trunk"},
	}

	got := declaredOrder(forest)
	want := []string{"synthetic-trunk", "synthetic-a", "synthetic-b", "synthetic-a-one", "synthetic-self", "synthetic-x", "synthetic-y"}
	if !slices.Equal(got, want) {
		t.Errorf("declaredOrder = %v, want %v", got, want)
	}
}

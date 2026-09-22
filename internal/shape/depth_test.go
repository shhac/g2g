package shape

import "testing"

// Depths is the one place every renderer asks how deep a node sits, so its
// contract is worth stating directly rather than only through two renderers.
func TestTreeDepthsSuppressesAChainAndMeasuresAFork(t *testing.T) {
	chain := map[string]string{"synthetic-b": "synthetic-a", "synthetic-c": "synthetic-b"}
	if depths := Depths([]string{"synthetic-a", "synthetic-b", "synthetic-c"}, lookup(chain)); len(depths) != 0 {
		t.Errorf("Depths(chain) = %v, want none", depths)
	}

	fork := map[string]string{"synthetic-a": "synthetic-trunk", "synthetic-a-one": "synthetic-a", "synthetic-b": "synthetic-trunk"}
	depths := Depths([]string{"synthetic-trunk", "synthetic-a", "synthetic-a-one", "synthetic-b"}, lookup(fork))
	for branch, want := range map[string]int{"synthetic-trunk": 0, "synthetic-a": 1, "synthetic-a-one": 2, "synthetic-b": 1} {
		if depths[branch] != want {
			t.Errorf("depth of %q = %d, want %d", branch, depths[branch], want)
		}
	}
}

// A parent outside the selection does not count: the selection's own roots sit
// at depth zero rather than hanging from something not on screen.
func TestTreeDepthsIgnoresAParentOutsideTheSelection(t *testing.T) {
	edges := map[string]string{"synthetic-a": "synthetic-elsewhere", "synthetic-b": "synthetic-a", "synthetic-c": "synthetic-a"}
	depths := Depths([]string{"synthetic-a", "synthetic-b", "synthetic-c"}, lookup(edges))
	if depths["synthetic-a"] != 0 {
		t.Errorf("selection root depth = %d, want 0", depths["synthetic-a"])
	}
}

func lookup(edges map[string]string) func(string) (string, bool) {
	return func(branch string) (string, bool) {
		parent, ok := edges[branch]
		return parent, ok && parent != ""
	}
}

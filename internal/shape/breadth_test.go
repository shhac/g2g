package shape

import (
	"slices"
	"testing"
)

func TestBreadthFirstTakesAGenerationAtATime(t *testing.T) {
	forest := Forest{Parents: map[string]string{
		"synthetic-a":     "synthetic-trunk",
		"synthetic-a-one": "synthetic-a",
		"synthetic-b":     "synthetic-trunk",
		"synthetic-other": "",
	}}

	got := forest.BreadthFirst([]string{"synthetic-trunk", "synthetic-other"})
	// Pre-order would put synthetic-a-one before synthetic-b; a generation at a
	// time puts every child of the trunk first.
	want := []string{"synthetic-trunk", "synthetic-other", "synthetic-a", "synthetic-b", "synthetic-a-one"}
	if !slices.Equal(got, want) {
		t.Errorf("BreadthFirst = %v, want %v", got, want)
	}
}

// A display parsed from another tool can name a branch as its own parent, or
// close a loop, and nothing downstream may then walk forever.
func TestBreadthFirstFinishesOnAnyShape(t *testing.T) {
	forest := Forest{Parents: map[string]string{
		"synthetic-self": "synthetic-self",
		"synthetic-x":    "synthetic-y",
		"synthetic-y":    "synthetic-x",
		"synthetic-z":    "synthetic-x",
	}}

	got := forest.BreadthFirst([]string{"synthetic-x", "synthetic-self", "synthetic-x"})
	want := []string{"synthetic-x", "synthetic-self", "synthetic-y", "synthetic-z"}
	if !slices.Equal(got, want) {
		t.Errorf("BreadthFirst = %v, want %v", got, want)
	}
}

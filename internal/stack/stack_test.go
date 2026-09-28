package stack

import (
	"maps"
	"testing"
)

// A recorded edge answers, and the base answers only for a branch the
// selection places under nothing, which an all scope's further trunks are.
// Every consumer asks this one method, so this is the rule for all of them.
func TestSitsOnPrefersTheRecordedParentAndFallsBackToTheBase(t *testing.T) {
	snapshot := Snapshot{
		Base:     "synthetic-main",
		Branches: []string{"synthetic-one", "synthetic-two", "synthetic-develop"},
		Parents:  map[string]string{"synthetic-one": "synthetic-main", "synthetic-two": "synthetic-one"},
	}
	for branch, want := range map[string]string{
		"synthetic-one":     "synthetic-main",
		"synthetic-two":     "synthetic-one",
		"synthetic-develop": "synthetic-main",
	} {
		if got := snapshot.SitsOn(branch); got != want {
			t.Errorf("SitsOn(%q) = %q, want %q", branch, got, want)
		}
	}
	want := map[string]string{
		"synthetic-main":    "",
		"synthetic-one":     "synthetic-main",
		"synthetic-two":     "synthetic-one",
		"synthetic-develop": "synthetic-main",
	}
	if got := snapshot.Shape().Parents; !maps.Equal(got, want) {
		t.Errorf("Shape() = %v, want %v", got, want)
	}
}

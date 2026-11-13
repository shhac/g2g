package stack

import (
	"maps"
	"strings"
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

// A refusal to project a fork names the leaves, each a --branch that turns the
// scope into one line, so the way out is something to paste rather than find.
func TestRequireLinearNamesTheLeavesToChooseFrom(t *testing.T) {
	forked := Snapshot{
		Target:   "synthetic-a",
		Base:     "main",
		Branches: []string{"synthetic-a", "synthetic-b", "synthetic-c", "synthetic-d", "synthetic-e"},
		Parents:  map[string]string{"synthetic-a": "main", "synthetic-b": "synthetic-a", "synthetic-c": "synthetic-b", "synthetic-d": "synthetic-b", "synthetic-e": "synthetic-d"},
	}
	err := forked.RequireLinear("land")
	if err == nil || !strings.Contains(err.Error(), `"synthetic-b" has more than one branch above it · select a leaf with --branch (synthetic-c, synthetic-e)`) {
		t.Fatalf("err = %v", err)
	}
	// The ways out are ways, not words inside the reason, so the column a
	// person reads and the line a machine reads name the same remedies.
	note := forked.LinearRepair("land")
	if strings.Contains(note.Reason, "--branch") || len(note.Ways) != 2 {
		t.Errorf("note = %+v, want the remedies as its two ways", note)
	}
	linear := forked
	linear.Branches, linear.Parents = forked.Branches[:3], map[string]string{"synthetic-a": "main", "synthetic-b": "synthetic-a", "synthetic-c": "synthetic-b"}
	if note := linear.LinearRepair("land"); note.Reason != "" || linear.RequireLinear("land") != nil {
		t.Errorf("a line is refused: %+v", note)
	}
}

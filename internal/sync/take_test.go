package sync

import (
	"strings"
	"testing"
)

// The boundary is a prefix of the stack, trunk first, and everything above it
// keeps the default — which is to refuse rather than pick a side. That refusal
// is the point: a boundary says where you have decided, not that you have
// decided everywhere.
func TestATakeAppliesThroughItsBoundaryAndNoFurther(t *testing.T) {
	branches := []string{"synthetic-main", "synthetic-a", "synthetic-b", "synthetic-c"}
	take, err := ParseTake("published", "synthetic-b")
	if err != nil {
		t.Fatal(err)
	}

	for branch, want := range map[string]bool{
		"synthetic-main": true,
		"synthetic-a":    true,
		"synthetic-b":    true,  // inclusive
		"synthetic-c":    false, // above it, so still refused
	} {
		if got := take.AppliesTo(branch, branches); got != want {
			t.Errorf("AppliesTo(%s) = %t, want %t", branch, got, want)
		}
	}
}

func TestAnUnboundedTakeAppliesToEverySelectedBranch(t *testing.T) {
	branches := []string{"synthetic-a", "synthetic-b"}
	take, err := ParseTake("published", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, branch := range branches {
		if !take.AppliesTo(branch, branches) {
			t.Errorf("AppliesTo(%s) = false, want the whole selection", branch)
		}
	}
}

// No side chosen means no branch is taken, boundary or not.
func TestNoSideTakesNothing(t *testing.T) {
	if TakeNothing.AppliesTo("synthetic-a", []string{"synthetic-a"}) {
		t.Error("the default resolved a divergence")
	}
}

// A boundary on a decision nobody made resolves nothing, and would silently do
// the ordinary thing instead of saying so.
func TestABoundaryWithoutASideIsRefused(t *testing.T) {
	_, err := ParseTake("", "synthetic-b")
	if err == nil {
		t.Fatal("ParseTake() error = nil, want a refusal")
	}
	for _, want := range []string{"--through", "no side was chosen", "--take published"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestAnUnknownSideIsRefusedAndNamesWhatItTakes(t *testing.T) {
	_, err := ParseTake("theirs", "")
	if err == nil {
		t.Fatal("ParseTake() error = nil, want a refusal")
	}
	for _, want := range []string{"theirs", "published"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

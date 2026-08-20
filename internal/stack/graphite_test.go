package stack

import (
	"reflect"
	"strings"
	"testing"
)

// SelectBoundary is the Graphite trunk rule: which declared trunk on an
// ancestry a selection hangs from. It was tested from stack_test.go, so the
// file named for the package tested one source's boundary and nothing else.

func TestSelectBoundaryUsesOnlyDeclaredGraphiteTrunks(t *testing.T) {
	path := []string{"main", "feature-one", "feature-two"}
	base, source, branches, err := SelectBoundary(path, []string{"main", "develop", "staging"}, "")
	if err != nil {
		t.Fatalf("SelectBoundary() error = %v", err)
	}
	if base != "main" || source != "Graphite-declared ancestry" || !reflect.DeepEqual(branches, []string{"feature-one", "feature-two"}) {
		t.Errorf("boundary = (%q, %q, %v)", base, source, branches)
	}
}

func TestSelectBoundaryRequiresOrValidatesTrunkOverride(t *testing.T) {
	path := []string{"develop", "main", "feature"}
	trunks := []string{"develop", "main", "staging"}
	if _, _, _, err := SelectBoundary(path, trunks, ""); err == nil || !strings.Contains(err.Error(), "multiple declared trunks") {
		t.Fatalf("SelectBoundary() error = %v, want ambiguity", err)
	}
	base, source, branches, err := SelectBoundary(path, trunks, "main")
	if err != nil {
		t.Fatalf("SelectBoundary() override error = %v", err)
	}
	if base != "main" || source != "--trunk" || !reflect.DeepEqual(branches, []string{"feature"}) {
		t.Errorf("override boundary = (%q, %q, %v)", base, source, branches)
	}
	for _, requested := range []string{"missing", "staging", "feature"} {
		if _, _, _, err := SelectBoundary(path, trunks, requested); err == nil {
			t.Errorf("SelectBoundary(%q) error = nil", requested)
		}
	}
}

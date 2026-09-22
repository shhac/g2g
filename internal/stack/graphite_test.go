package stack

import (
	"strings"
	"testing"
)

// selectBoundary is the Graphite trunk rule: which declared trunk on an
// ancestry a selection hangs from. What the selection then holds above that
// trunk is graphiteBoundary's, and the parity table checks it.

func TestSelectBoundaryUsesOnlyDeclaredGraphiteTrunks(t *testing.T) {
	path := []string{"synthetic-main", "synthetic-one", "synthetic-two"}
	base, source, err := selectBoundary(path, []string{"synthetic-main", "synthetic-develop", "synthetic-staging"}, "")
	if err != nil {
		t.Fatalf("selectBoundary() error = %v", err)
	}
	if base != "synthetic-main" || source != "Graphite-declared ancestry" {
		t.Errorf("boundary = (%q, %q)", base, source)
	}
}

func TestSelectBoundaryRequiresOrValidatesTrunkOverride(t *testing.T) {
	path := []string{"synthetic-develop", "synthetic-main", "synthetic-feature"}
	trunks := []string{"synthetic-develop", "synthetic-main", "synthetic-staging"}
	if _, _, err := selectBoundary(path, trunks, ""); err == nil || !strings.Contains(err.Error(), "multiple declared trunks") {
		t.Fatalf("selectBoundary() error = %v, want ambiguity", err)
	}
	base, source, err := selectBoundary(path, trunks, "synthetic-main")
	if err != nil {
		t.Fatalf("selectBoundary() override error = %v", err)
	}
	if base != "synthetic-main" || source != "--trunk" {
		t.Errorf("override boundary = (%q, %q)", base, source)
	}
	for _, requested := range []string{"synthetic-missing", "synthetic-staging", "synthetic-feature"} {
		if _, _, err := selectBoundary(path, trunks, requested); err == nil {
			t.Errorf("selectBoundary(%q) error = nil", requested)
		}
	}
}

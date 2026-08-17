// Tests for the shared renderer in view.go, which had no test file of its own
// and was covered incidentally by a command's tests.
package cli

import (
	"testing"
)

func TestTreePrefixesDeriveConnectorsFromDepthAlone(t *testing.T) {
	nodes := []stackNode{
		{Branch: "root"},
		{Branch: "first", Depth: 1},
		{Branch: "first-child", Depth: 2},
		{Branch: "first-last", Depth: 2},
		{Branch: "last", Depth: 1},
		{Branch: "last-child", Depth: 2},
	}

	got := treePrefixes(nodes)

	want := []string{"", forkGlyph, railGlyph + " " + forkGlyph, railGlyph + " " + lastGlyph, lastGlyph, "  " + lastGlyph}
	for index := range want {
		if got[index] != want[index] {
			t.Errorf("prefix[%d] (%s) = %q, want %q", index, nodes[index].Branch, got[index], want[index])
		}
	}
}

// A view carrying no depth is the linear case every other command produces,
// and it must render exactly as it always has.
func TestTreePrefixesAreEmptyForALinearView(t *testing.T) {
	got := treePrefixes([]stackNode{{Branch: "a"}, {Branch: "b"}, {Branch: "c"}})

	for index, prefix := range got {
		if prefix != "" {
			t.Errorf("prefix[%d] = %q, want empty", index, prefix)
		}
	}
}

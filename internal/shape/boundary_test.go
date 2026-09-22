package shape_test

import (
	"os/exec"
	"strings"
	"testing"
)

// shape depends on nothing, and that is the whole reason it is a package.
//
// The scope vocabulary and the forest traversal moved here out of stack
// because stack reaches Graphite and GitHub, so internal/graph importing a
// scope constant pulled both in through an import line that named neither.
// graph's own boundary test would catch this package regaining a dependency
// only as a failure in graph; this says where the fault is.
func TestShapeDependsOnNothingInternal(t *testing.T) {
	const pkg = "github.com/shhac/g2g/internal/shape"
	out, err := exec.Command("go", "list", "-deps", pkg).Output()
	if err != nil {
		t.Skipf("go list unavailable: %v", err)
	}
	for _, dep := range strings.Fields(string(out)) {
		if strings.HasPrefix(dep, "github.com/shhac/g2g/") && dep != pkg {
			t.Errorf("%s reaches %s · it must depend on nothing internal, which is what lets every package use it", pkg, dep)
		}
	}
}

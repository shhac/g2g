package landed_test

import (
	"os/exec"
	"strings"
	"testing"
)

// landed asks its question through the interface it declares and depends on
// nothing, which is what lets graph, prune and link share one answer to "has
// this landed" without any of them reaching the others — or reaching Git's
// client, which graph must not.
func TestLandedDependsOnNothingInternal(t *testing.T) {
	const pkg = "github.com/shhac/g2g/internal/landed"
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

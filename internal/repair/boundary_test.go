package repair_test

import (
	"os/exec"
	"strings"
	"testing"
)

// repair depends on nothing, which is what lets internal/graph describe its
// own refusals without reaching Graphite or GitHub. A dependency here would
// arrive in graph transitively, where its boundary test would report it
// against the wrong package.
func TestRepairDependsOnNothingInternal(t *testing.T) {
	const pkg = "github.com/shhac/g2g/internal/repair"
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

package graph_test

import (
	"os/exec"
	"strings"
	"testing"
)

// This package must depend on Git alone. Importing Graphite or GitHub into it,
// or making any of graph/track/untrack need a network, removes the only reason
// it exists — and that is stated in AGENTS.md and in the repository skill.
//
// It was violated anyway, and invisibly: the scope vocabulary and the forest
// traversal lived in stack, stack reaches both Graphite and GitHub, so
// importing a scope constant pulled the whole of both in transitively. Nothing
// looked wrong at any single import line, which is exactly why the check has
// to be over the transitive set.
func TestGraphDependsOnGitAlone(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "github.com/shhac/g2g/internal/graph").Output()
	if err != nil {
		t.Skipf("go list unavailable: %v", err)
	}

	// The packages this may reach, and no more than it does reach. Anything
	// that speaks to another tool or to a network belongs to a caller, not
	// here — and that includes Git's own client: this package asks Git through
	// the Ancestry interface it declares, which is what keeps every decision in
	// it testable without a process. The list once permitted internal/git and
	// internal/subprocess although nothing imported either, so the check would
	// have let the first such import through unremarked.
	permitted := map[string]bool{
		"github.com/shhac/g2g/internal/graph":      true,
		"github.com/shhac/g2g/internal/shape":      true,
		"github.com/shhac/g2g/internal/parallel":   true,
		"github.com/shhac/g2g/internal/landed":     true,
		"github.com/shhac/g2g/internal/repair":     true,
		"github.com/shhac/g2g/internal/diagnostic": true,
	}
	for _, dep := range strings.Fields(string(out)) {
		if !strings.HasPrefix(dep, "github.com/shhac/g2g/") {
			continue
		}
		if !permitted[dep] {
			t.Errorf("internal/graph reaches %s · it may depend on Git alone, and this one arrives transitively rather than at an import line", dep)
		}
	}
}

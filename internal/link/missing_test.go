package link

import (
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/stack"
)

func TestMissingLocalRootNeedsRestorationRatherThanPruning(t *testing.T) {
	for _, missing := range []string{"synthetic-main", "synthetic-work"} {
		plan := Plan{Discovery: stack.Discovery{Snapshot: stack.Snapshot{
			Source: stack.SourceG2G, Target: "synthetic-work", Base: "synthetic-main", Scope: stack.ScopeStack,
			Ancestry: []string{"synthetic-main", "synthetic-work"}, Absent: []string{missing},
		}}}
		note, _ := plan.Repair()
		if got := strings.Contains(note.Sentence(), "g2g prune"); got != (missing == "synthetic-work") {
			t.Fatalf("wrong cleanup advice for %s: %+v", missing, note)
		}
		if missing == "synthetic-main" && !strings.Contains(note.Sentence(), "restore synthetic-main locally") {
			t.Fatalf("missing root has no restoration advice: %+v", note)
		}
	}
}

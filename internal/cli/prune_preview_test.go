package cli

import (
	"testing"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/prune"
)

// A branch with no commits of its own is forgotten under that name, not as
// landed: it may be one nobody has committed to yet, and calling it landed is
// how someone is invited to forget work they are about to start.
func TestPruneCallsAnEmptyBranchWhatGraphCallsIt(t *testing.T) {
	plan := prune.Plan{Discovery: graph.Discovery{States: map[string]graph.NodeState{
		"synthetic-empty":  graph.StateEmpty,
		"synthetic-landed": graph.StateLanded,
	}}}
	if got := forgetState(plan, "synthetic-empty"); got != "no commits of its own · forget" {
		t.Errorf("empty branch reads %q", got)
	}
	if got := forgetState(plan, "synthetic-landed"); got != "landed · forget" {
		t.Errorf("landed branch reads %q", got)
	}
}

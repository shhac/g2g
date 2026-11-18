package cli_test

import (
	"strings"
	"testing"
)

// A scope pull does not take is refused with what does narrow a pull, since
// those two are flags of their own rather than scopes.
func TestPullRefusesANarrowerScopeWithWhatNarrowsAPull(t *testing.T) {
	for _, scope := range []string{"path", "branch", "subtree"} {
		_, _, err := run(t, "pull", "--scope", scope)
		if err == nil {
			t.Fatalf("pull --scope %s was accepted", scope)
		}
		for _, want := range []string{"want stack, trunk", "--trunk-only", "--above <branch>", "descendants included"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("pull --scope %s refusal lacks %q: %v", scope, want, err)
			}
		}
	}
}

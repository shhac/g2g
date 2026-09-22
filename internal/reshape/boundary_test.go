package reshape_test

import (
	"os/exec"
	"strings"
	"testing"
)

// Reshaping acts on g2g's own graph and on local Git, and nothing it does
// needs a network. Reaching Graphite or GitHub from here would make deleting a
// branch depend on a tool the graph exists to do without.
func TestReshapeReachesNeitherGraphiteNorGitHub(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "github.com/shhac/g2g/internal/reshape").Output()
	if err != nil {
		t.Skipf("go list unavailable: %v", err)
	}
	for _, dep := range strings.Fields(string(out)) {
		for _, forbidden := range []string{"/internal/graphite", "/internal/githubstack", "/internal/stack", "/internal/restack"} {
			if strings.HasSuffix(dep, forbidden) {
				t.Errorf("internal/reshape reaches %s", dep)
			}
		}
	}
}

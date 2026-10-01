package cli

import (
	"strings"
	"testing"
)

func TestTrunkOnlyPullRefusesOptionsThatWouldChangeItsBoundary(t *testing.T) {
	for _, extra := range [][]string{{"--scope", "stack"}, {"--take", "published"}, {"--through", "synthetic-auth"}, {"--prune"}} {
		t.Run(strings.Join(extra, " "), func(t *testing.T) {
			out, err := runPull(t, &syncCLIGit{}, &syncCLIRestack{}, &pruneCLIGit{}, append([]string{"pull", "--trunk-only"}, extra...)...)
			if err == nil || !strings.Contains(err.Error(), "trunk-only") {
				t.Fatalf("error = %v, output = %s", err, out)
			}
		})
	}
}

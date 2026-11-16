package link

import (
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/stack"
)

func TestAbsorbedCleanupOrdersCollectionReplayAndSourceCleanup(t *testing.T) {
	for _, source := range []stack.Source{stack.SourceG2G, stack.SourceGraphite, stack.SourceGitHub} {
		snapshot := stack.Snapshot{Source: source, Branches: []string{"synthetic-child", "synthetic-top"}, Parents: map[string]string{"synthetic-child": "synthetic-parent", "synthetic-top": "synthetic-child"}}
		ways := AbsorbedWays(snapshot, "synthetic-child", true, "synthetic-$remote")
		switch source {
		case stack.SourceG2G:
			if len(ways) != 3 || ways[0].Command != "g2g pull --branch synthetic-child --remote 'synthetic-$remote' --above synthetic-parent" || ways[1].Command != "g2g restack --branch synthetic-child" || ways[2].Command != "g2g prune --branch synthetic-child --scope branch" {
				t.Fatalf("wrong cleanup sequence: %+v", ways)
			}
		case stack.SourceGraphite:
			if len(ways) != 1 || ways[0].Command != "gt sync" {
				t.Fatalf("wrong source cleanup: %+v", ways)
			}
		case stack.SourceGitHub:
			if len(ways) != 1 || ways[0].Command != "" || !strings.Contains(ways[0].Effect, "wherever") {
				t.Fatalf("invented cleanup: %+v", ways)
			}
		}
	}
}

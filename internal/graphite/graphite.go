// Package graphite reads Graphite's supported, noninteractive stack display.
package graphite

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/shhac/gt2gh/internal/subprocess"
)

// SupportedVersion is the Graphite CLI version whose display grammar is
// accepted by this package. A different version fails closed rather than being
// guessed at.
const SupportedVersion = "1.8.6"

// Stack is one declared-trunk-to-selected Graphite path, ordered bottom to top.
// Trunk is deliberately excluded from Branches.
type Stack struct {
	Trunk    string
	Branches []string
}

// Client discovers Graphite stacks through its supported CLI, never through
// Graphite's private configuration or metadata.
type Client struct {
	Runner subprocess.Runner
}

// Discover resolves selected without checking it out. It accepts a forked
// Graphite tree and returns only the selected branch's deterministic ancestry.
func (c Client) Discover(ctx context.Context, selected string) (Stack, error) {
	graph, err := c.read(ctx)
	if err != nil {
		return Stack{}, err
	}

	node, ok := graph.nodes[selected]
	if !ok {
		return Stack{}, fmt.Errorf("Graphite does not track local branch %q", selected)
	}
	if selected == graph.trunk {
		return Stack{}, fmt.Errorf("selected branch %q is the Graphite trunk; select a branch above %q", selected, graph.trunk)
	}

	var reversed []string
	seen := make(map[string]bool)
	for node.name != graph.trunk {
		if seen[node.name] {
			return Stack{}, fmt.Errorf("Graphite display contains an ancestry cycle at %q", node.name)
		}
		seen[node.name] = true
		reversed = append(reversed, node.name)
		if node.parent == "" {
			return Stack{}, fmt.Errorf("Graphite display has no path from %q to declared trunk %q", selected, graph.trunk)
		}
		node = graph.nodes[node.parent]
	}
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	return Stack{Trunk: graph.trunk, Branches: reversed}, nil
}

// TrackedBranches returns stable, local Graphite branch candidates for shell
// completion. It neither checks out nor changes a branch.
func (c Client) TrackedBranches(ctx context.Context) ([]string, error) {
	graph, err := c.read(ctx)
	if err != nil {
		return nil, err
	}
	branches := make([]string, 0, len(graph.nodes)-1)
	for branch := range graph.nodes {
		if branch != graph.trunk {
			branches = append(branches, branch)
		}
	}
	sort.Strings(branches)
	return branches, nil
}

func (c Client) read(ctx context.Context) (graph, error) {
	if c.Runner == nil {
		return graph{}, fmt.Errorf("Graphite runner is not configured")
	}
	version, err := c.Runner.Run(ctx, "gt", "--version")
	if err != nil {
		return graph{}, commandError("gt --version", err, version)
	}
	if got := strings.TrimSpace(string(version)); got != SupportedVersion {
		return graph{}, fmt.Errorf("unsupported Graphite CLI version %q; gt2gh supports display grammar from %s only", got, SupportedVersion)
	}

	output, err := c.Runner.Run(ctx, "gt", "log", "--all", "--reverse", "--no-interactive")
	if err != nil {
		return graph{}, commandError("gt log --all --reverse --no-interactive", err, output)
	}
	return parseLog(string(output))
}

func commandError(command string, err error, output []byte) error {
	message := strings.TrimSpace(string(output))
	if message == "" {
		return fmt.Errorf("%s failed: %w", command, err)
	}
	return fmt.Errorf("%s failed: %w (%s)", command, err, message)
}

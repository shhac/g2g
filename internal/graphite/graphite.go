// Package graphite reads Graphite's supported, noninteractive stack display.
package graphite

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/shhac/gt2gh/internal/diagnostic"
	"github.com/shhac/gt2gh/internal/subprocess"
)

// SupportedVersion is the Graphite CLI version whose display grammar is
// accepted by this package. A different version fails closed rather than being
// guessed at.
const SupportedVersion = "1.8.6"

// Stack is one complete Graphite-declared ancestry, ordered from the display
// root through the selected branch. The compact display does not identify one
// universal link base when repositories configure multiple trunk-like branches,
// so base selection is intentionally left to the link service.
type Stack struct {
	Path   []string
	Trunks []string
}

// Client discovers Graphite stacks through its supported CLI, never through
// Graphite's private configuration or metadata.
type Client struct {
	Runner subprocess.Runner
}

// Discover resolves selected without checking it out. It accepts a forked
// Graphite tree and returns only the selected branch's deterministic ancestry.
func (c Client) Discover(ctx context.Context, selected string) (Stack, error) {
	return c.DiscoverStack(ctx, selected, false)
}

// DiscoverStack resolves selected's declared ancestry and, when includeTip is
// true, extends through the only unambiguous downward child chain. It never
// checks out, changes, or otherwise manages a Graphite branch.
func (c Client) DiscoverStack(ctx context.Context, selected string, includeTip bool) (Stack, error) {
	graph, err := c.read(ctx)
	if err != nil {
		return Stack{}, err
	}

	node, ok := graph.nodes[selected]
	if !ok {
		return Stack{}, fmt.Errorf("Graphite does not track local branch %q", selected)
	}
	var reversed []string
	seen := make(map[string]bool)
	for {
		if seen[node.name] {
			return Stack{}, fmt.Errorf("Graphite display contains an ancestry cycle at %q", node.name)
		}
		seen[node.name] = true
		reversed = append(reversed, node.name)
		if node.parent == "" {
			break
		}
		parent, ok := graph.nodes[node.parent]
		if !ok {
			return Stack{}, fmt.Errorf("Graphite display has missing parent %q for %q", node.parent, node.name)
		}
		node = parent
	}
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	if includeTip {
		children := graphChildren(graph)
		for current := selected; ; {
			next := children[current]
			switch len(next) {
			case 0:
				goto expanded
			case 1:
				reversed = append(reversed, next[0])
				current = next[0]
			default:
				return Stack{}, fmt.Errorf("selected Graphite branch %q has multiple descendants (%s); full-stack resolution requires one linear path (rerun with --no-stack to stop at the selected branch)", current, strings.Join(next, ", "))
			}
		}
	}

expanded:
	diagnostic.Event(ctx, "graphite.path",
		diagnostic.Field{Key: "selected", Value: selected},
		diagnostic.Field{Key: "path", Value: strings.Join(reversed, " -> ")},
		diagnostic.Field{Key: "full_stack", Value: strconv.FormatBool(includeTip)},
		diagnostic.Field{Key: "declared_trunks", Value: strings.Join(graph.roots, ",")},
	)
	return Stack{Path: reversed, Trunks: append([]string(nil), graph.roots...)}, nil
}

func graphChildren(g graph) map[string][]string {
	children := make(map[string][]string, len(g.nodes))
	for _, candidate := range g.nodes {
		if candidate.parent != "" {
			children[candidate.parent] = append(children[candidate.parent], candidate.name)
		}
	}
	for parent := range children {
		sort.Strings(children[parent])
	}
	return children
}

// TrackedBranches returns stable, local Graphite branch candidates for shell
// completion. It neither checks out nor changes a branch.
func (c Client) TrackedBranches(ctx context.Context) ([]string, error) {
	graph, err := c.read(ctx)
	if err != nil {
		return nil, err
	}
	branches := make([]string, 0, len(graph.nodes))
	for branch := range graph.nodes {
		branches = append(branches, branch)
	}
	sort.Strings(branches)
	diagnostic.Event(ctx, "graphite.tracked_branches", diagnostic.Field{Key: "count", Value: fmt.Sprintf("%d", len(branches))})
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
	diagnostic.Event(ctx, "graphite.version", diagnostic.Field{Key: "version", Value: SupportedVersion}, diagnostic.Field{Key: "supported", Value: "true"})

	output, err := c.Runner.Run(ctx, "gt", "log", "short", "--all", "--reverse", "--no-interactive")
	if err != nil {
		return graph{}, commandError("gt log short --all --reverse --no-interactive", err, output)
	}
	graph, err := parseLog(string(output))
	if err == nil {
		diagnostic.Event(ctx, "graphite.discovery", diagnostic.Field{Key: "command", Value: "gt log short --all --reverse --no-interactive"}, diagnostic.Field{Key: "roots", Value: fmt.Sprintf("%d", len(graph.roots))})
	}
	return graph, err
}

func commandError(command string, err error, output []byte) error {
	message := strings.TrimSpace(string(output))
	if message == "" {
		return fmt.Errorf("%s failed: %w", command, err)
	}
	return fmt.Errorf("%s failed: %w (%s)", command, err, message)
}

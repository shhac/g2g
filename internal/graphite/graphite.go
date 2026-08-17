// Package graphite reads Graphite's supported, noninteractive stack display.
package graphite

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/subprocess"
)

// KnownVersion is the Graphite CLI version whose compact display grammar has
// direct fixture coverage. Compatible patch and minor versions warn, then rely
// on the strict parser to reject any grammar drift.
const KnownVersion = "1.8.6"

const supportedMajor = 1

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

// Discover resolves selected's declared ancestry without checking it out. It
// accepts a forked Graphite tree and returns only the deterministic line down
// to the trunk.
//
// How much of the forest a command acts on is a scope, decided above this
// package. Discovery used to take a bool that also extended through the one
// unambiguous downward chain, which is why a branch with two children was
// refused outright — a bool cannot express the shapes people actually have.
func (c Client) Discover(ctx context.Context, selected string) (Stack, error) {
	parsed, err := c.read(ctx)
	if err != nil {
		return Stack{}, err
	}
	stack, err := resolveStack(parsed, selected)
	if err != nil {
		return Stack{}, err
	}
	diagnostic.Event(ctx, "graphite.path",
		diagnostic.Field{Key: "selected", Value: selected},
		diagnostic.Field{Key: "path", Value: strings.Join(stack.Path, " -> ")},
		diagnostic.Field{Key: "declared_trunks", Value: strings.Join(stack.Trunks, ",")},
	)
	return stack, nil
}

func resolveStack(parsed graph, selected string) (Stack, error) {
	node, ok := parsed.nodes[selected]
	if !ok {
		return Stack{}, fmt.Errorf("Graphite does not track local branch %q", selected)
	}
	path, err := ancestry(parsed, node)
	if err != nil {
		return Stack{}, err
	}
	return Stack{Path: path, Trunks: append([]string(nil), parsed.roots...)}, nil
}

func ancestry(parsed graph, node node) ([]string, error) {
	var reversed []string
	seen := make(map[string]bool)
	for {
		if seen[node.name] {
			return nil, fmt.Errorf("Graphite display contains an ancestry cycle at %q", node.name)
		}
		seen[node.name] = true
		reversed = append(reversed, node.name)
		if node.parent == "" {
			break
		}
		parent, ok := parsed.nodes[node.parent]
		if !ok {
			return nil, fmt.Errorf("Graphite display has missing parent %q for %q", node.parent, node.name)
		}
		node = parent
	}
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	return reversed, nil
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

// gate is the compatibility check every Graphite command passes through,
// reads and writes alike. A version this build has not been tested against is
// worth a warning before parsing output; it is worth the same warning before
// writing, where the cost of a changed contract is someone else's metadata.
func (c Client) gate(ctx context.Context) error {
	if c.Runner == nil {
		return fmt.Errorf("Graphite runner is not configured")
	}
	version, err := c.run(ctx, "--version")
	if err != nil {
		return err
	}
	got, known, err := checkVersion(version)
	if err != nil {
		return err
	}
	if !known {
		diagnostic.Warn(ctx, "graphite-version", fmt.Sprintf("Graphite CLI version %s is not a known supported version; attempting compact display parsing and it will fail safely if the output changed", got))
	}
	diagnostic.Event(ctx, "graphite.version", diagnostic.Field{Key: "version", Value: got}, diagnostic.Field{Key: "known", Value: strconv.FormatBool(known)}, diagnostic.Field{Key: "compatible_major", Value: "true"})
	return nil
}

// run invokes Graphite and reports a failure as the command that produced it.
// Four call sites were each rebuilding the same command string to wrap the
// error, which is four chances to word it differently.
func (c Client) run(ctx context.Context, arguments ...string) ([]byte, error) {
	output, err := c.Runner.Run(ctx, "gt", arguments...)
	if err != nil {
		return nil, commandError("gt "+strings.Join(arguments, " "), err, output)
	}
	return output, nil
}

func (c Client) read(ctx context.Context) (graph, error) {
	if err := c.gate(ctx); err != nil {
		return graph{}, err
	}
	output, err := c.run(ctx, "log", "short", "--all", "--reverse", "--no-interactive")
	if err != nil {
		return graph{}, err
	}
	graph, err := parseLog(string(output))
	if err == nil {
		diagnostic.Event(ctx, "graphite.discovery", diagnostic.Field{Key: "command", Value: "gt log short --all --reverse --no-interactive"}, diagnostic.Field{Key: "roots", Value: fmt.Sprintf("%d", len(graph.roots))})
	}
	return graph, err
}

func checkVersion(output []byte) (string, bool, error) {
	version := strings.TrimSpace(string(output))
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return "", false, fmt.Errorf("unrecognized Graphite CLI version output %q; expected major.minor.patch", version)
	}
	values := make([]int, len(parts))
	for index, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 || strconv.Itoa(value) != part {
			return "", false, fmt.Errorf("unrecognized Graphite CLI version output %q; expected major.minor.patch", version)
		}
		values[index] = value
	}
	if values[0] != supportedMajor {
		return "", false, fmt.Errorf("unsupported Graphite CLI major version %d; g2g supports major version %d", values[0], supportedMajor)
	}
	return version, version == KnownVersion, nil
}

// commandError keeps a failed Graphite invocation actionable while holding its
// output to the same bounded, redacted treatment as every other diagnostic.
func commandError(command string, err error, output []byte) error {
	message := diagnostic.BoundedOutput(output)
	if message == "" {
		return fmt.Errorf("%s failed: %w", command, err)
	}
	return fmt.Errorf("%s failed: %w (%s)", command, err, message)
}

// repoConfigName is the file Graphite writes when a repository starts using
// it. Only its existence is ever consulted.
const repoConfigName = ".graphite_repo_config"

// Configured reports whether this repository already uses Graphite.
//
// This is the one place g2g looks at a Graphite-owned path, and it is a
// deliberate, narrow exception to reading none of them. The alternative is
// worse: Graphite's discovery command creates state in a repository that has
// never used it, so merely asking "does Graphite describe this branch?" would
// enrol a repository whose owner chose not to. Existence is all that is read —
// never contents, and never for structure.
func Configured(ctx context.Context, git CommonDirReader) (bool, error) {
	if git == nil {
		return false, nil
	}
	common, err := git.CommonDir(ctx)
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(filepath.Join(common, repoConfigName)); err != nil {
		return false, nil
	}
	return true, nil
}

// CommonDirReader supplies the Git common directory, which linked worktrees
// share, so this answers the same way from any of them.
type CommonDirReader interface {
	CommonDir(context.Context) (string, error)
}

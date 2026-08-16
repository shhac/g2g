package graphite

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/shhac/gt2gh/internal/diagnostic"
)

// Forest is everything Graphite declares about the repository: each tracked
// branch's parent, and the branches it shows as roots.
//
// Discovery answers about one ancestry because that is all a projection needs.
// Aligning two graphs needs both of them whole, which is the only reason this
// exists alongside Stack.
type Forest struct {
	// Parents maps a branch to its declared parent. A root maps to "".
	Parents map[string]string
	Roots   []string
}

// Branches returns every branch the forest names, sorted.
func (f Forest) Branches() []string {
	branches := make([]string, 0, len(f.Parents))
	for branch := range f.Parents {
		branches = append(branches, branch)
	}
	sort.Strings(branches)
	return branches
}

// Children returns the branches declared under branch, sorted. Untracking
// cascades to them, so a caller that removes anything has to know them.
func (f Forest) Children(branch string) []string {
	children := make([]string, 0)
	for candidate, parent := range f.Parents {
		if parent == branch {
			children = append(children, candidate)
		}
	}
	sort.Strings(children)
	return children
}

// ReadForest returns Graphite's whole declared forest.
func (c Client) ReadForest(ctx context.Context) (Forest, error) {
	parsed, err := c.read(ctx)
	if err != nil {
		return Forest{}, err
	}
	forest := Forest{Parents: make(map[string]string, len(parsed.nodes)), Roots: append([]string(nil), parsed.roots...)}
	for name, node := range parsed.nodes {
		forest.Parents[name] = node.parent
	}
	diagnostic.Event(ctx, "graphite.forest",
		diagnostic.Field{Key: "branches", Value: fmt.Sprintf("%d", len(forest.Parents))},
		diagnostic.Field{Key: "roots", Value: fmt.Sprintf("%d", len(forest.Roots))},
	)
	return forest, nil
}

// Track records branch under parent in Graphite.
//
// The branch is named explicitly rather than checked out. Graphite defaults to
// the current branch when the argument is omitted, and a command that walked a
// forest by checking out each branch would be a worse thing than the drift it
// is fixing.
//
// Graphite requires parent to be tracked already, so callers write parents
// before children.
func (c Client) Track(ctx context.Context, branch, parent string) error {
	if err := safeArguments(branch, parent); err != nil {
		return err
	}
	if err := c.gate(ctx); err != nil {
		return err
	}
	diagnostic.Event(ctx, "graphite.track", diagnostic.Field{Key: "branch", Value: branch}, diagnostic.Field{Key: "parent", Value: parent})
	_, err := c.run(ctx, "track", branch, "--parent", parent, "--no-interactive")
	return err
}

// Untrack removes branch from Graphite.
//
// Graphite untracks the whole subtree beneath it, which --force only stops it
// asking about. Deciding that the cascade is acceptable belongs to the caller,
// which is the only place that knows what else it is about to align.
func (c Client) Untrack(ctx context.Context, branch string) error {
	if err := safeArguments(branch); err != nil {
		return err
	}
	if err := c.gate(ctx); err != nil {
		return err
	}
	diagnostic.Event(ctx, "graphite.untrack", diagnostic.Field{Key: "branch", Value: branch})
	_, err := c.run(ctx, "untrack", branch, "--force", "--no-interactive")
	return err
}

// safeArguments refuses names a shell-free exec would still hand to Graphite as
// options. The read path validates the same way before passing a branch to gh.
func safeArguments(names ...string) error {
	for _, name := range names {
		if name == "" {
			return fmt.Errorf("branch name is empty")
		}
		if strings.HasPrefix(name, "-") {
			return fmt.Errorf("branch %q cannot be passed safely to gt", name)
		}
	}
	return nil
}

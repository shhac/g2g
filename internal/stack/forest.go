package stack

import (
	"fmt"
	"sort"
)

// Forest is structure with the source removed: which branch hangs from which,
// and nothing else.
//
// Both records produce one. The g2g store knows its edges directly; Graphite's
// compact display is parsed into the same shape. Keeping the traversal here,
// once, is what makes a scope mean the same thing whichever record answered —
// the alternative is two implementations of "subtree" that agree until the day
// they do not.
type Forest struct {
	// Parents maps a branch to the branch it hangs from. A branch mapped to the
	// empty string, or absent while appearing as someone's parent, is a root.
	// Both records need that latitude: the g2g store does not record an edge for
	// a trunk, and Graphite's display gives its roots an empty parent.
	Parents map[string]string
}

// Parent reports the recorded parent and whether there is one.
func (f Forest) Parent(branch string) (string, bool) {
	parent, ok := f.Parents[branch]
	if !ok || parent == "" {
		return "", false
	}
	return parent, true
}

// Branches is every branch the forest mentions, as a child or as a parent,
// sorted. A root that nothing is recorded against still counts.
func (f Forest) Branches() []string {
	seen := make(map[string]bool, len(f.Parents)*2)
	for branch, parent := range f.Parents {
		seen[branch] = true
		if parent != "" {
			seen[parent] = true
		}
	}
	branches := make([]string, 0, len(seen))
	for branch := range seen {
		branches = append(branches, branch)
	}
	sort.Strings(branches)
	return branches
}

// Children returns the branches recorded directly under branch, sorted so a
// render is stable across runs.
func (f Forest) Children(branch string) []string {
	children := make([]string, 0)
	for candidate, parent := range f.Parents {
		if parent == branch && candidate != branch {
			children = append(children, candidate)
		}
	}
	sort.Strings(children)
	return children
}

// Roots returns every branch nothing sits under, sorted.
func (f Forest) Roots() []string {
	roots := make([]string, 0)
	for _, branch := range f.Branches() {
		if _, hasParent := f.Parent(branch); !hasParent {
			roots = append(roots, branch)
		}
	}
	return roots
}

// Path returns the root-to-branch chain, inclusive of both ends.
func (f Forest) Path(branch string) ([]string, error) {
	path := []string{branch}
	seen := map[string]bool{branch: true}
	for current := branch; ; {
		parent, hasParent := f.Parent(current)
		if !hasParent {
			break
		}
		if seen[parent] {
			return nil, fmt.Errorf("branch %q is part of a parent cycle", branch)
		}
		seen[parent] = true
		path = append(path, parent)
		current = parent
	}
	for left, right := 0, len(path)-1; left < right; left, right = left+1, right-1 {
		path[left], path[right] = path[right], path[left]
	}
	return path, nil
}

// Subtree returns branch and every descendant in a stable pre-order: a parent
// always precedes its children and siblings come in sorted order.
func (f Forest) Subtree(branch string) []string {
	return f.subtree(branch, map[string]bool{})
}

// subtree carries the visited set so a cycle cannot recurse forever. Path
// reports a cycle because it can see the whole chain; walking downward can only
// stop, which is the safe thing for a renderer to do.
func (f Forest) subtree(branch string, seen map[string]bool) []string {
	if seen[branch] {
		return nil
	}
	seen[branch] = true
	subtree := []string{branch}
	for _, child := range f.Children(branch) {
		subtree = append(subtree, f.subtree(child, seen)...)
	}
	return subtree
}

// Component returns the whole tree branch belongs to, starting from its root.
func (f Forest) Component(branch string) ([]string, error) {
	path, err := f.Path(branch)
	if err != nil {
		return nil, err
	}
	return f.Subtree(path[0]), nil
}

// All returns every root and everything under it, in root order.
func (f Forest) All() []string {
	seen := make(map[string]bool)
	branches := make([]string, 0, len(f.Parents)+1)
	for _, root := range f.Roots() {
		for _, branch := range f.Subtree(root) {
			if seen[branch] {
				continue
			}
			seen[branch] = true
			branches = append(branches, branch)
		}
	}
	return branches
}

// Select returns the branches a scope covers, in render order.
func (f Forest) Select(branch string, scope Scope) ([]string, error) {
	switch scope {
	case ScopeBranch:
		return []string{branch}, nil
	case ScopePath:
		return f.Path(branch)
	case ScopeSubtree:
		return f.Subtree(branch), nil
	case ScopeStack:
		return f.Stack(branch)
	case ScopeTrunk:
		return f.Component(branch)
	case ScopeAll:
		return f.All(), nil
	default:
		return nil, fmt.Errorf("unsupported scope %q", scope)
	}
}

// Stack is the trunk, the branch, and everything above it: what a person means
// by "my stack". The cousins that merely share a trunk are excluded, which is
// the only thing separating it from Component.
//
// Ordered trunk-first so a parent always precedes its children, which is what
// every renderer and every replay assumes.
func (f Forest) Stack(branch string) ([]string, error) {
	path, err := f.Path(branch)
	if err != nil {
		return nil, err
	}
	// Path ends at branch and Subtree starts at it, so one of the two copies is
	// dropped rather than deduplicated after the fact.
	return append(path, f.Subtree(branch)[1:]...), nil
}

// Restrict returns the edges among the selected branches only. A parent outside
// the selection is dropped rather than dangling, which is what lets a renderer
// treat the selection's own roots as roots.
func (f Forest) Restrict(branches []string) map[string]string {
	selected := make(map[string]bool, len(branches))
	for _, branch := range branches {
		selected[branch] = true
	}
	edges := make(map[string]string, len(branches))
	for _, branch := range branches {
		if parent, ok := f.Parent(branch); ok && selected[parent] {
			edges[branch] = parent
		}
	}
	return edges
}

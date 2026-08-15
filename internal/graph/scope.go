package graph

import (
	"fmt"
	"strings"
)

// Scope selects how much of the forest a command operates on.
//
// This is a graph-selection concept and is deliberately separate from
// projection policy: selecting a subtree for display does not imply a subtree
// can be projected onto a GitHub native stack, which is still linear.
//
// A boolean --tree was the obvious alternative and is worse. It frames tree
// operation as the exception, and it cannot express "this branch and
// everything under it" — which is the scope someone actually wants while
// working on one sub-stack of a larger tree.
type Scope string

const (
	// ScopeBranch is the selected branch alone.
	ScopeBranch Scope = "branch"
	// ScopePath is root to selected branch. It is the default because it is
	// what a linear GitHub projection consumes.
	ScopePath Scope = "path"
	// ScopeSubtree is the selected branch and every descendant.
	ScopeSubtree Scope = "subtree"
	// ScopeGraph is the whole tree the selected branch belongs to.
	ScopeGraph Scope = "graph"
)

// Scopes lists every accepted value in the order they widen, which is also the
// order shell completion offers them.
var Scopes = []Scope{ScopeBranch, ScopePath, ScopeSubtree, ScopeGraph}

// ParseScope validates a flag value. An empty value is the default.
func ParseScope(value string) (Scope, error) {
	if value == "" {
		return ScopePath, nil
	}
	for _, scope := range Scopes {
		if Scope(value) == scope {
			return scope, nil
		}
	}
	names := make([]string, 0, len(Scopes))
	for _, scope := range Scopes {
		names = append(names, string(scope))
	}
	return "", fmt.Errorf("unsupported scope %q (want %s)", value, strings.Join(names, ", "))
}

// Select returns the branches a scope covers, in render order.
func (g Graph) Select(branch string, scope Scope) ([]string, error) {
	switch scope {
	case ScopeBranch:
		return []string{branch}, nil
	case ScopePath:
		return g.Path(branch)
	case ScopeSubtree:
		return g.Subtree(branch), nil
	case ScopeGraph:
		return g.Component(branch)
	default:
		return nil, fmt.Errorf("unsupported scope %q", scope)
	}
}

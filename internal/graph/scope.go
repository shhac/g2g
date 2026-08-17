package graph

import (
	"fmt"

	"github.com/shhac/g2g/internal/stack"
)

// Scope and its values are defined in stack, because both sources answer a
// scope now and the word has to mean the same thing asked of either. These
// aliases keep the graph-side vocabulary intact for callers that never touch a
// Graphite path.
type Scope = stack.Scope

const (
	ScopeBranch  = stack.ScopeBranch
	ScopePath    = stack.ScopePath
	ScopeSubtree = stack.ScopeSubtree
	ScopeGraph   = stack.ScopeGraph
	ScopeForest  = stack.ScopeForest
)

// Scopes is the set every selecting command accepts. ReadScopes adds forest,
// which only read-only commands offer.
var (
	Scopes     = stack.Scopes
	ReadScopes = stack.ReadScopes
)

// ParseScope validates a flag value against the scopes a selecting command
// accepts. An empty value is the default.
func ParseScope(value string) (Scope, error) { return stack.ParseScope(value, stack.Scopes) }

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
	case ScopeForest:
		// A repository with nothing recorded has no roots, and returning
		// nothing would render a header over an empty space. The branch that
		// was asked about is still the honest answer.
		if forest := g.Forest(); len(forest) != 0 {
			return forest, nil
		}
		return []string{branch}, nil
	default:
		return nil, fmt.Errorf("unsupported scope %q", scope)
	}
}

// Forest is every root and everything under it, in root order. It is the only
// selection that does not start from the selected branch, which is exactly why
// it answers "show me everything" and why no mutating command accepts it.
func (g Graph) Forest() []string {
	seen := make(map[string]bool)
	branches := make([]string, 0, len(g.Edges)+1)
	for _, root := range g.Roots() {
		for _, branch := range g.Subtree(root) {
			if seen[branch] {
				continue
			}
			seen[branch] = true
			branches = append(branches, branch)
		}
	}
	return branches
}

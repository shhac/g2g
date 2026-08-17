package graph

import (
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
	ScopeStack   = stack.ScopeStack
	ScopeTrunk   = stack.ScopeTrunk
	ScopeAll     = stack.ScopeAll
)

// Scopes is the set every selecting command accepts. ReadScopes adds forest,
// which only read-only commands offer.
var (
	Scopes        = stack.Scopes
	ReadScopes    = stack.ReadScopes
	RewriteScopes = stack.RewriteScopes
	ProjectScopes = stack.ProjectScopes
)

// ParseScope validates a flag value against the scopes a selecting command
// accepts, defaulting to the whole stack.
func ParseScope(value string) (Scope, error) {
	return stack.ParseScope(value, stack.Scopes, stack.ScopeStack)
}

// Select returns the branches a scope covers, in render order.
//
// The scope values are stack.Forest's to know. A second switch over the same
// names here would drift the moment one gains a value the other lacks, which is
// exactly what happened before the traversal was shared.
func (g Graph) Select(branch string, scope Scope) ([]string, error) {
	selected, err := g.shape().Select(branch, scope)
	if err != nil {
		return nil, err
	}
	// A repository with nothing recorded has no roots, so the widest scopes
	// select nothing and would render a header over empty space. The branch
	// that was asked about is still the honest answer.
	if len(selected) == 0 {
		return []string{branch}, nil
	}
	return selected, nil
}

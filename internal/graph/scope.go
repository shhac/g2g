package graph

import (
	"github.com/shhac/g2g/internal/shape"
)

// Scope and its values are defined in shape, because both sources answer a
// scope now and the word has to mean the same thing asked of either. These
// aliases keep the graph-side vocabulary intact for callers that never touch a
// Graphite path.
//
// shape depends on nothing, which is what lets this package keep depending on
// Git alone. Taking them from stack instead pulled Graphite and GitHub in
// transitively.
type Scope = shape.Scope

const (
	ScopeBranch  = shape.ScopeBranch
	ScopePath    = shape.ScopePath
	ScopeSubtree = shape.ScopeSubtree
	ScopeStack   = shape.ScopeStack
	ScopeTrunk   = shape.ScopeTrunk
	ScopeAll     = shape.ScopeAll
)

// Scopes is the set every selecting command accepts. ReadScopes adds all,
// which only read-only commands offer, and RewriteScopes drops the widest two,
// which want deliberate worktree handling first.
//
// There is no ParseScope here on purpose. Parsing needs the accepted set and
// the default together, and both genuinely differ per command; a graph-side
// wrapper could only supply one pair, which is how one command's default
// silently becomes another's.
var (
	Scopes        = shape.Scopes
	ReadScopes    = shape.ReadScopes
	RewriteScopes = shape.RewriteScopes
)

// Select returns the branches a scope covers, in render order.
//
// The scope values are shape.Forest's to know. A second switch over the same
// names here would drift the moment one gains a value the other lacks, which is
// exactly what happened before the traversal was shared.
func (g Graph) Select(branch string, scope Scope) ([]string, error) {
	selected, err := g.Shape().Select(branch, scope)
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

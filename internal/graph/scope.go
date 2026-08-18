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

// The accepted sets are not aliased here, and neither is ParseScope. A command
// names the set it offers, and there is one place to read that list from; a
// second name for it is how two of the four ended up aliased in one package
// and two in another, each used from exactly one call site.

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

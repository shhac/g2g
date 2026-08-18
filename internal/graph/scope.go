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

// Scopes is the set every selecting command accepts. ReadScopes adds all,
// which only read-only commands offer, and RewriteScopes drops the widest two,
// which want deliberate worktree handling first.
//
// There is no ParseScope here on purpose. Parsing needs the accepted set and
// the default together, and both genuinely differ per command; a graph-side
// wrapper could only supply one pair, which is how one command's default
// silently becomes another's.
var (
	Scopes        = stack.Scopes
	ReadScopes    = stack.ReadScopes
	RewriteScopes = stack.RewriteScopes
)

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

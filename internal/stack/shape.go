package stack

import (
	"github.com/shhac/g2g/internal/shape"
)

// Scope and Forest live in shape, which depends on nothing.
//
// They were defined here, and that quietly cost internal/graph the one
// property it exists for. graph must depend on Git alone; it needs the scope
// vocabulary and the traversal, so it imported stack — and stack imports
// Graphite and GitHub, so graph transitively did too. The invariant was stated
// in two places and held in neither.
//
// These are aliases rather than a second vocabulary: shape.Scope and
// stack.Scope are the same type, so every existing caller keeps working and
// there is still exactly one definition of each.
type (
	Scope  = shape.Scope
	Forest = shape.Forest
)

const (
	ScopeBranch  = shape.ScopeBranch
	ScopePath    = shape.ScopePath
	ScopeSubtree = shape.ScopeSubtree
	ScopeStack   = shape.ScopeStack
	ScopeTrunk   = shape.ScopeTrunk
	ScopeAll     = shape.ScopeAll
)

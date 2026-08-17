package stack

import (
	"fmt"
	"strings"
)

// Scope selects how much of the structure a command operates on.
//
// This is a selection concept and is deliberately separate from projection
// policy: selecting a subtree for display does not imply a subtree can be
// projected onto a GitHub native stack, which is still linear. Commands that
// mutate or project therefore accept only the linear scopes, and say so.
//
// It lives here rather than in graph because both sources answer it now. The
// g2g store and Graphite describe the same shape of thing, and a scope asked of
// one must mean exactly what it means asked of the other.
//
// A boolean --tree was the obvious alternative and is worse. It frames tree
// operation as the exception, and it cannot express "this branch and everything
// under it" — which is the scope someone actually wants while working on one
// sub-stack of a larger tree.
type Scope string

const (
	// ScopeBranch is the selected branch alone.
	ScopeBranch Scope = "branch"
	// ScopePath is root to selected branch. It is the default because it is
	// what a linear GitHub projection consumes.
	ScopePath Scope = "path"
	// ScopeSubtree is the selected branch and every descendant.
	ScopeSubtree Scope = "subtree"
	// ScopeGraph is the one tree the selected branch belongs to. It stops at
	// that tree's root: a repository with several roots has several graphs, and
	// widening this value to mean all of them would silently widen every
	// mutating command that already accepts it.
	ScopeGraph Scope = "graph"
	// ScopeForest is every root and everything under it, whether or not the
	// selected branch can reach it. It is the only scope that can answer "show
	// me everything", and it is offered by read-only commands alone.
	ScopeForest Scope = "forest"
)

// Scopes lists the scopes every selecting command accepts, in the order they
// widen, which is also the order shell completion offers them. ScopeForest is
// deliberately absent: it is added per command by the read-only ones.
var Scopes = []Scope{ScopeBranch, ScopePath, ScopeSubtree, ScopeGraph}

// ReadScopes is Scopes plus ScopeForest, for commands that only ever display.
var ReadScopes = []Scope{ScopeBranch, ScopePath, ScopeSubtree, ScopeGraph, ScopeForest}

// Linear reports whether a scope can only ever produce one ordered path.
// Projection onto a GitHub native stack and any history rewrite require it.
func (s Scope) Linear() bool { return s == ScopeBranch || s == ScopePath }

// ParseScope validates a flag value against the scopes a command accepts. An
// empty value is the default. Passing the accepted set rather than consulting a
// global is what lets one command offer forest and another refuse it, with the
// refusal naming what that command actually takes.
func ParseScope(value string, accepted []Scope) (Scope, error) {
	if len(accepted) == 0 {
		accepted = Scopes
	}
	if value == "" {
		return ScopePath, nil
	}
	names := make([]string, 0, len(accepted))
	for _, scope := range accepted {
		if Scope(value) == scope {
			return scope, nil
		}
		names = append(names, string(scope))
	}
	return "", fmt.Errorf("unsupported scope %q (want %s)", value, strings.Join(names, ", "))
}

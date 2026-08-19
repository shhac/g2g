package shape

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
	// ScopeStack is the trunk, the selected branch, and everything above it —
	// what a person means by "my stack". It excludes the cousins that merely
	// share a trunk, which is what separates it from ScopeTrunk.
	ScopeStack Scope = "stack"
	// ScopeTrunk is the selected branch's trunk and everything under it,
	// cousins included. "The trunk moved, bring everything on it up to date."
	ScopeTrunk Scope = "trunk"
	// ScopeAll is every trunk's stacks. It exists so a repository with several
	// trunks can be seen whole, which is a reading problem: nothing that
	// mutates offers it.
	ScopeAll Scope = "all"
)

// Scopes lists the scopes a command may offer, in the order they widen, which
// is also the order shell completion offers them. ScopeAll is deliberately
// absent: it is added per command by the read-only ones.
var Scopes = []Scope{ScopeBranch, ScopePath, ScopeSubtree, ScopeStack, ScopeTrunk}

// ReadScopes is Scopes plus ScopeAll, for commands that only ever display.
var ReadScopes = []Scope{ScopeBranch, ScopePath, ScopeSubtree, ScopeStack, ScopeTrunk, ScopeAll}

// RewriteScopes is what a command that rewrites history may offer. all is
// absent because it spans trunks, and a rewrite acts on one.
var RewriteScopes = []Scope{ScopeBranch, ScopePath, ScopeSubtree, ScopeStack, ScopeTrunk}

// SyncScopes is what sync may offer, and it is deliberately only the two.
//
// sync advances the base and replays what sits on it, so anything narrower than
// the whole stack is incoherent: replaying a subtree leaves the branches below
// it on the old base, and the subtree's own fork point did not move, so the
// replay would do nothing at all. The only meaningful widening is trunk —
// "the trunk moved, bring every stack on it up to date".
var SyncScopes = []Scope{ScopeStack, ScopeTrunk}

// ProjectScopes is what a command that projects onto a GitHub native stack may
// offer. A native stack is linear, so these are the two that can produce one —
// and stack still refuses when it forks.
var ProjectScopes = []Scope{ScopeStack, ScopePath}

// Linear reports whether a scope can only ever produce one ordered path
// regardless of the shape it is asked about. ScopeStack can produce one too,
// but only when the selection happens not to fork, which is a property of the
// repository rather than of the scope.
func (s Scope) Linear() bool { return s == ScopeBranch || s == ScopePath }

// ParseScope validates a flag value against the scopes a command accepts,
// falling back to that command's own default when none was given.
//
// Both the accepted set and the default are arguments rather than globals,
// because they genuinely differ: status defaults to stack because reading is
// free, restack defaults to subtree because rewriting is not, and only a
// read-only command offers all. A shared default is how one of those silently
// becomes another.
func ParseScope(value string, accepted []Scope, fallback Scope) (Scope, error) {
	if len(accepted) == 0 {
		accepted = Scopes
	}
	if value == "" {
		return fallback, nil
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

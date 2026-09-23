package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/shape"
)

// scopeOptions is the branch-and-scope half of every command's selection.
//
// The two option types that carry it — one producing a graph.Selection, one a
// stack.Selection — differ only in what they build at the end. They had
// separate copies of the flag, the completion, the default, and the validator,
// and the copies had already drifted: only one of them was actually called
// before discovery, so the projection commands accepted a scope they did not
// offer until something deep in selection happened to notice.
type scopeOptions struct {
	branch string
	scope  string
	// accepted is the scope set this command registered, empty for a command
	// that never offered one, and fallback is what it means when none is
	// given. Both are per command: all is a legitimate value for a read-only
	// view and a dangerous one to hand a command that rewrites, and reading
	// defaults wider than rewriting does.
	accepted []shape.Scope
	fallback shape.Scope
}

// effectiveScope is the scope this invocation means, which is the command's own
// default when the flag was not given.
func (o scopeOptions) effectiveScope() shape.Scope {
	if o.scope == "" {
		return o.fallback
	}
	return shape.Scope(o.scope)
}

// validateScope rejects a scope this command does not offer. Cobra validates a
// flag's syntax, never its vocabulary, so without this a command silently
// accepts any scope the service happens to parse.
func (o scopeOptions) validateScope() error {
	_, err := shape.ParseScope(o.scope, o.accepted, o.fallback)
	return err
}

// registerScope adds the scope flag to a command that accepts one. It is a
// separate call rather than an empty argument, because a command without a
// scope should say so by not asking for one.
func (o *scopeOptions) registerScope(cmd *cobra.Command, scopes []shape.Scope, fallback shape.Scope, usage string) {
	o.accepted, o.fallback = scopes, fallback
	cmd.Flags().StringVar(&o.scope, "scope", "", usage)
	_ = cmd.RegisterFlagCompletionFunc("scope", completionCallback(staticCompletions(scopes)))
}

// staticCompletions offers exactly the values a command registered, which is
// what keeps completion from proposing a scope the same command would refuse.
func staticCompletions(scopes []shape.Scope) func(context.Context, string) ([]string, error) {
	return func(context.Context, string) ([]string, error) {
		values := make([]string, 0, len(scopes))
		for _, scope := range scopes {
			values = append(values, string(scope))
		}
		return values, nil
	}
}

// scopeUsage writes the help for a scope flag from the values the command
// actually accepts, so a command cannot advertise a scope it would refuse or
// omit one it takes. verb is what the command does with the selection.
func scopeUsage(verb string, scopes []graph.Scope) string {
	meaning := map[graph.Scope]string{
		graph.ScopeBranch:  "just this branch",
		graph.ScopePath:    "the trunk down to this branch",
		graph.ScopeSubtree: "this branch and everything above it",
		graph.ScopeStack:   "this whole stack, trunk to tips",
		graph.ScopeTrunk:   "every stack on this trunk",
		graph.ScopeAll:     "every stack in the repository",
	}
	parts := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		parts = append(parts, fmt.Sprintf("%s (%s)", scope, meaning[scope]))
	}
	return "how much to " + verb + ": " + strings.Join(parts, ", ")
}

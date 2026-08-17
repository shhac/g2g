package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/stack"
)

// stackOptions owns the common target-selection flags shared by stack commands.
// It stays in cli because flag wording and shell completion are presentation
// concerns; services receive the resulting value object, not Cobra state.
type stackOptions struct {
	branch  string
	trunk   string
	noStack bool
	from    string
	scope   string
	// accepted is the scope set this command registered, empty for a command
	// that never offered one, and fallback is what it means when none is given.
	// Only read-only commands take a scope that can fork: a tree cannot be
	// projected onto a GitHub native stack, and widening what is shown must not
	// widen what is done.
	accepted []stack.Scope
	fallback stack.Scope
}

func (o stackOptions) Selection() stack.Selection {
	scope := stack.Scope(o.scope)
	if scope == "" {
		scope = o.fallback
	}
	return stack.Selection{Branch: o.branch, Trunk: o.trunk, NoStack: o.noStack, Scope: scope, From: stack.Source(o.from)}
}

// registerScope adds the scope flag to a command that accepts one. It is a
// separate call from register, so a command without a scope says so by not
// asking for one.
func (o *stackOptions) registerScope(cmd *cobra.Command, scopes []stack.Scope, fallback stack.Scope, usage string) {
	o.accepted, o.fallback = scopes, fallback
	cmd.Flags().StringVar(&o.scope, "scope", "", usage)
	_ = cmd.RegisterFlagCompletionFunc("scope", completionCallback(func(context.Context, string) ([]string, error) {
		values := make([]string, 0, len(scopes))
		for _, scope := range scopes {
			values = append(values, string(scope))
		}
		return values, nil
	}))
}

// validateScope rejects a scope this command does not offer, and rejects
// combining it with the older boolean rather than silently preferring one.
func (o stackOptions) validateScope() error {
	if o.scope == "" {
		return nil
	}
	if o.noStack {
		return fmt.Errorf("--scope and --no-stack both select how much to show; --no-stack means --scope %s", stack.ScopeBranch)
	}
	_, err := stack.ParseScope(o.scope, o.accepted, o.fallback)
	return err
}

func (o *stackOptions) register(cmd *cobra.Command, completions stack.Completions, branchUsage, trunkUsage string) {
	cmd.Flags().StringVar(&o.branch, "branch", "", branchUsage)
	cmd.Flags().StringVar(&o.trunk, "trunk", "", trunkUsage)
	cmd.Flags().BoolVar(&o.noStack, "no-stack", false, "stop at the selected branch instead of resolving the full linear stack")
	cmd.Flags().StringVar(&o.from, "from", "", "read the structure from this source only (default: whichever describes the branch)")
	_ = cmd.RegisterFlagCompletionFunc("branch", completionCallback(completions.Branches))
	_ = cmd.RegisterFlagCompletionFunc("trunk", completionCallback(func(ctx context.Context, prefix string) ([]string, error) {
		return completions.Trunks(ctx, o.branch, prefix)
	}))
	_ = cmd.RegisterFlagCompletionFunc("from", completionCallback(sourceCompletions))
}

// sourceCompletions offers the sources a build can read from. It is a fixed
// list rather than one derived from the resolver: completion must not depend on
// repository state to name a flag's own vocabulary.
func sourceCompletions(_ context.Context, prefix string) ([]string, error) {
	var matches []string
	for _, source := range []stack.Source{stack.SourceG2G, stack.SourceGraphite} {
		if strings.HasPrefix(string(source), prefix) {
			matches = append(matches, string(source))
		}
	}
	return matches, nil
}

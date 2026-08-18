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
	branch string
	trunk  string
	from   string
	scope  string
	// accepted is the scope set this command registered, empty for a command
	// that never offered one, and fallback is what it means when none is given.
	// Only read-only commands take a scope that can fork: a tree cannot be
	// projected onto a GitHub native stack, and widening what is shown must not
	// widen what is done.
	accepted []stack.Scope
	fallback stack.Scope
	// sources are the records this command may be pointed at. It is per command
	// for the same reason the scope set is: reading a pull request base invokes
	// gh, and push must never do that, so the flag has to refuse what the
	// command cannot honour rather than resolving it and finding out.
	sources []stack.Source
}

func (o stackOptions) Selection() stack.Selection {
	scope := stack.Scope(o.scope)
	if scope == "" {
		scope = o.fallback
	}
	return stack.Selection{Branch: o.branch, Trunk: o.trunk, Scope: scope, From: stack.Source(o.from)}
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

// validate rejects a scope or a source this command does not offer, before any
// discovery runs. Both are per command, and both were previously checked either
// late or not at all: a bad scope surfaced from deep inside selection, and a
// source the command had promised not to reach was simply honoured.
func (o stackOptions) validate() error {
	if err := o.validateScope(); err != nil {
		return err
	}
	return o.validateSource()
}

// validateScope rejects a scope this command does not offer.
func (o stackOptions) validateScope() error {
	_, err := stack.ParseScope(o.scope, o.accepted, o.fallback)
	return err
}

func (o *stackOptions) register(cmd *cobra.Command, completions stack.Completions, sources []stack.Source, branchUsage, trunkUsage string) {
	o.sources = sources
	names := make([]string, 0, len(sources))
	for _, source := range sources {
		names = append(names, string(source))
	}
	cmd.Flags().StringVar(&o.branch, "branch", "", branchUsage)
	cmd.Flags().StringVar(&o.trunk, "trunk", "", trunkUsage)
	cmd.Flags().StringVar(&o.from, "from", "", "read the structure from this source only: "+strings.Join(names, ", ")+" (default: whichever describes the branch)")
	_ = cmd.RegisterFlagCompletionFunc("branch", completionCallback(completions.Branches))
	_ = cmd.RegisterFlagCompletionFunc("trunk", completionCallback(func(ctx context.Context, prefix string) ([]string, error) {
		return completions.Trunks(ctx, o.branch, prefix)
	}))
	_ = cmd.RegisterFlagCompletionFunc("from", completionCallback(func(_ context.Context, prefix string) ([]string, error) {
		matches := make([]string, 0, len(names))
		for _, name := range names {
			if strings.HasPrefix(name, prefix) {
				matches = append(matches, name)
			}
		}
		return matches, nil
	}))
}

// validateSource refuses a record this command cannot be pointed at.
//
// Without it the resolver happily honours --from for any source it holds,
// including the on-request tier, so push --from pull-request invoked gh before
// selection began — breaking the one contract that command has, and doing so
// through a flag registered on every stack command by default.
func (o stackOptions) validateSource() error {
	if o.from == "" || stack.Permits(o.sources, stack.Source(o.from)) {
		return nil
	}
	names := make([]string, 0, len(o.sources))
	for _, source := range o.sources {
		names = append(names, string(source))
	}
	// A name no build has is a different mistake from a real source this
	// command cannot reach, and the remedy differs: fix the spelling, or use a
	// command that may read it. Answering both with one message would send a
	// typo looking for an invariant.
	if !stack.Permits(stack.ReadableSources, stack.Source(o.from)) {
		return fmt.Errorf("unknown source %q · this command reads %s", o.from, strings.Join(names, ", "))
	}
	return fmt.Errorf("this command cannot read structure from %q · it takes %s, because reading a pull request base means invoking gh and this command never does",
		o.from, strings.Join(names, " or "))
}

// sourceCompletions offers the sources a build can read from. It is a fixed
// list rather than one derived from the resolver: completion must not depend on
// repository state to name a flag's own vocabulary.
func sourceCompletions(_ context.Context, prefix string) ([]string, error) {
	var matches []string
	for _, source := range []stack.Source{stack.SourceG2G, stack.SourceGraphite, stack.SourcePullRequest} {
		if strings.HasPrefix(string(source), prefix) {
			matches = append(matches, string(source))
		}
	}
	return matches, nil
}

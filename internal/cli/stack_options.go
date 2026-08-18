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
	scopeOptions
	trunk string
	from  string
	// sources are the records this command may be pointed at. It is per command
	// for the same reason the scope set is: reading a pull request base invokes
	// gh, and push must never do that, so the flag has to refuse what the
	// command cannot honour rather than resolving it and finding out.
	sources []stack.Source
}

func (o stackOptions) Selection() stack.Selection {
	return stack.Selection{Branch: o.branch, Trunk: o.trunk, Scope: o.effectiveScope(), From: stack.Source(o.from)}
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

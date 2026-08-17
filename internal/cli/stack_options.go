package cli

import (
	"context"
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
}

func (o stackOptions) Selection() stack.Selection {
	return stack.Selection{Branch: o.branch, Trunk: o.trunk, NoStack: o.noStack, From: stack.Source(o.from)}
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

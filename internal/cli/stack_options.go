package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/shhac/gt2gh/internal/stack"
)

// stackOptions owns the common target-selection flags shared by stack commands.
// It stays in cli because flag wording and shell completion are presentation
// concerns; services receive the resulting value object, not Cobra state.
type stackOptions struct {
	branch  string
	trunk   string
	noStack bool
}

func (o stackOptions) Selection() stack.Selection {
	return stack.Selection{Branch: o.branch, Trunk: o.trunk, NoStack: o.noStack}
}

func (o *stackOptions) register(cmd *cobra.Command, completions stack.Completions, branchUsage, trunkUsage string) {
	cmd.Flags().StringVar(&o.branch, "branch", "", branchUsage)
	cmd.Flags().StringVar(&o.trunk, "trunk", "", trunkUsage)
	cmd.Flags().BoolVar(&o.noStack, "no-stack", false, "stop at the selected branch instead of resolving the full linear stack")
	_ = cmd.RegisterFlagCompletionFunc("branch", completionCallback(completions.Branches))
	_ = cmd.RegisterFlagCompletionFunc("trunk", completionCallback(func(ctx context.Context, prefix string) ([]string, error) {
		return completions.Trunks(ctx, o.branch, prefix)
	}))
}

package cli

import (
	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/graph"
)

// graphOptions owns the selection flags the graph commands share. Scope is a
// graph-selection concept and stays separate from projection policy: choosing
// to display a subtree does not imply a subtree can be linked on GitHub.
type graphOptions struct {
	scopeOptions
}

func (o graphOptions) Selection() graph.Selection {
	return graph.Selection{Branch: o.branch, Scope: o.effectiveScope()}
}

// registerBranch adds the selector every graph command shares. Completion comes
// from the store, which reads one file and runs nothing.
func (o *graphOptions) registerBranch(cmd *cobra.Command, service graph.Service) {
	cmd.Flags().StringVar(&o.branch, "branch", "", "branch to select (defaults to current branch)")
	_ = cmd.RegisterFlagCompletionFunc("branch", completionCallback(localBranchCompletions(service)))
}

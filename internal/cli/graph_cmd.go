package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/shape"
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

func newGraph(service graph.Service, presentation Presentation) *cobra.Command {
	var selection graphOptions
	cmd := &cobra.Command{Use: "graph", GroupID: groupStructure, Short: "Inspect the branch graph g2g owns, independently of Graphite (read-only)", Args: cobra.NoArgs}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		presentation := presentation.resolve(cmd)
		if err := selection.validateScope(); err != nil {
			return err
		}
		ctx, cancel := newBudgets(cmd).discovery(commandContext(cmd.Context(), cmd, "graph", "read_only", selection.branch, ""))
		defer cancel()
		discovery, err := service.Discover(ctx, selection.Selection())
		if err != nil {
			return err
		}
		return writeGraphView(cmd.OutOrStdout(), graphStatusView(discovery), discovery, presentation)
	}
	selection.registerBranch(cmd, service)
	selection.registerScope(cmd, shape.ReadScopes, graph.ScopeStack, scopeUsage("show", shape.ReadScopes))
	return cmd
}

func applyMode(apply bool) string {
	if apply {
		return "apply"
	}
	return "preview"
}

func localBranchCompletions(service graph.Service) func(context.Context, string) ([]string, error) {
	return func(ctx context.Context, prefix string) ([]string, error) {
		if service.Git == nil {
			return nil, nil
		}
		return service.Git.LocalBranches(ctx)
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

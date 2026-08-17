package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/stack"
)

// graphOptions owns the selection flags the graph commands share. Scope is a
// graph-selection concept and stays separate from projection policy: choosing
// to display a subtree does not imply a subtree can be linked on GitHub.
type graphOptions struct {
	branch string
	scope  string
	// accepted is the scope set this command registered. It is kept so the
	// refusal can name what this command takes, rather than what some other
	// command would have allowed: forest is a legitimate value to offer a
	// read-only view and a dangerous one to hand a command that rewrites.
	accepted []graph.Scope
}

func (o graphOptions) Selection() graph.Selection {
	return graph.Selection{Branch: o.branch, Scope: graph.Scope(o.scope)}
}

// validateScope rejects a scope this command does not offer. Cobra validates a
// flag's syntax, never its vocabulary, so without this a command silently
// accepts any scope the service happens to parse.
func (o graphOptions) validateScope() error {
	_, err := stack.ParseScope(o.scope, o.accepted)
	return err
}

// registerBranch adds the selector every graph command shares.
func (o *graphOptions) registerBranch(cmd *cobra.Command, service graph.Service) {
	cmd.Flags().StringVar(&o.branch, "branch", "", "branch to select (defaults to current branch)")
	_ = cmd.RegisterFlagCompletionFunc("branch", completionCallback(localBranchCompletions(service)))
}

// registerScope adds the scope flag for the commands that have one. It is a
// separate call rather than an empty argument, because a command without a
// scope should say so by not asking for one.
func (o *graphOptions) registerScope(cmd *cobra.Command, scopes []graph.Scope, usage string) {
	o.accepted = scopes
	cmd.Flags().StringVar(&o.scope, "scope", "", usage)
	_ = cmd.RegisterFlagCompletionFunc("scope", completionCallback(staticCompletions(scopes)))
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
	selection.registerScope(cmd, graph.ReadScopes, "how much to show: branch, path, subtree, graph (the tree this branch is in), or forest (every stack)")
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

func staticCompletions(scopes []graph.Scope) func(context.Context, string) ([]string, error) {
	return func(context.Context, string) ([]string, error) {
		values := make([]string, 0, len(scopes))
		for _, scope := range scopes {
			values = append(values, string(scope))
		}
		return values, nil
	}
}

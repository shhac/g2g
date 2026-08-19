package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
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

func newGraph(service graph.Service, selector stack.PathSelector, completions stack.Completions, presentation Presentation) *cobra.Command {
	var selection graphOptions
	var from string
	cmd := &cobra.Command{Use: "graph", GroupID: groupStructure, Short: "Inspect a branch graph, from g2g's own store or another record (read-only)", Args: cobra.NoArgs}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		presentation := presentation.resolve(cmd)
		if err := selection.validateScope(); err != nil {
			return err
		}
		if err := validateOfflineSource(from); err != nil {
			return err
		}
		ctx, cancel := newBudgets(cmd).discovery(commandContext(cmd.Context(), cmd, "graph", "read_only", selection.branch, ""))
		defer cancel()
		if source := stack.Source(from); source != "" && source != stack.SourceG2G {
			return writeSourceGraph(ctx, cmd, selector, selection, source, presentation)
		}
		discovery, err := service.Discover(ctx, selection.Selection())
		if err != nil {
			return err
		}
		return writeGraphView(cmd.OutOrStdout(), graphStatusView(discovery), discovery, presentation)
	}
	// Only the records that need no network. Reading a pull request base means
	// invoking gh, and this command answering without one is the whole reason
	// it exists separately from status.
	cmd.Flags().StringVar(&from, "from", "", "read the structure from this record only: g2g, graphite (default: g2g's own store)")
	_ = cmd.RegisterFlagCompletionFunc("from", completionCallback(func(_ context.Context, prefix string) ([]string, error) {
		matches := make([]string, 0, 2)
		for _, source := range stack.OfflineSources {
			if strings.HasPrefix(string(source), prefix) {
				matches = append(matches, string(source))
			}
		}
		return matches, nil
	}))
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

// validateOfflineSource refuses a record this command would have to reach the
// network for, before any discovery runs.
//
// graph answers without a network, which is the whole reason it exists apart
// from status. A pull request base is read by invoking gh, so it is not a
// record this command can offer however useful the comparison would be.
func validateOfflineSource(from string) error {
	if from == "" || stack.Permits(stack.OfflineSources, stack.Source(from)) {
		return nil
	}
	if stack.Permits(stack.ReadableSources, stack.Source(from)) {
		return fmt.Errorf("g2g graph cannot read structure from %q · it takes g2g or graphite, because reading a pull request base means invoking gh and this command answers without a network · g2g status --from %s does read it", from, from)
	}
	return fmt.Errorf("unknown source %q · g2g graph reads g2g or graphite", from)
}

// writeSourceGraph renders a tree that another record describes, in the shape
// g2g's own graph is drawn in.
//
// Seeing both in one format is what makes a divergence visible: the parity
// table compares the records on synthetic fixtures, and this compares them on
// the repository in front of you.
func writeSourceGraph(ctx context.Context, cmd *cobra.Command, selector stack.PathSelector, selection graphOptions, source stack.Source, p Presentation) error {
	if selector == nil {
		return fmt.Errorf("this build has no source resolver, so it can only read g2g's own store")
	}
	snapshot, err := selector.Select(ctx, stack.Selection{
		Branch: selection.branch,
		Scope:  selection.effectiveScope(),
		From:   source,
	}, "g2g graph")
	if err != nil {
		return err
	}
	return writeStackView(cmd.OutOrStdout(), structureNote(sourceGraphView(snapshot), snapshot), p)
}

// sourceGraphView draws the shape and nothing else.
//
// The state g2g's own view annotates — needs restack, moved off parent, fork
// point lost — is computed from recorded fork points, which only g2g's store
// has. Another record describes where branches sit and cannot describe whether
// their contents have drifted, so this says the first and stays silent on the
// second rather than inventing an answer.
func sourceGraphView(snapshot stack.Snapshot) stackView {
	ordered := append([]string{snapshot.Base}, snapshot.Branches...)
	depths := treeDepths(ordered, snapshot.ParentOf)
	nodes := []stackNode{{Branch: snapshot.Base, Trunk: true}}
	for _, branch := range snapshot.Branches {
		nodes = append(nodes, stackNode{
			Branch: branch,
			Target: branch == snapshot.Target,
			Parent: snapshot.Parents[branch],
			Depth:  depths[branch],
		})
	}
	return stackView{Operation: "graph", Target: snapshot.Target, TargetSource: snapshot.TargetSource, Nodes: nodes}
}

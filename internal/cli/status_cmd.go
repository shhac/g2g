package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/push"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
)

func newStatus(service graph.Service, selector stack.PathSelector, published push.Known, presentation Presentation) *cobra.Command {
	var selection graphOptions
	var from, remote string
	cmd := &cobra.Command{Use: "status", GroupID: groupLook, Short: "Show the stack you are on and where each branch stands (read-only, offline)", Args: cobra.NoArgs}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		presentation := presentation.resolve(cmd)
		if err := selection.validateScope(); err != nil {
			return err
		}
		if err := validateOfflineSource(from); err != nil {
			return err
		}
		ctx, cancel := newBudgets(cmd).discovery(commandContext(cmd.Context(), cmd, "read_only", selection.branch, ""))
		defer cancel()
		if source := stack.Source(from); source != "" && source != stack.SourceG2G {
			return writeSourceGraph(ctx, cmd, selector, selection, source, presentation)
		}
		discovery, err := service.Discover(ctx, selection.Selection())
		if err != nil {
			return err
		}
		publishing, err := readPublished(ctx, published, remote, cmd.Flags().Changed("remote"), discovery)
		if err != nil {
			return err
		}
		return writeGraphView(cmd.OutOrStdout(), markPublished(statusView(discovery), remote, publishing), discovery, presentation)
	}
	cmd.Flags().StringVar(&remote, "remote", "origin", "the remote whose last-known branches each one is compared with")
	// Only the records that need no network. Reading a pull request base means
	// invoking gh, and this command answering without one is the whole reason
	// it exists separately from github status.
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

// validateOfflineSource refuses a record this command would have to reach the
// network for, before any discovery runs.
//
// status answers without a network, which is the whole reason it exists apart
// from github status. A pull request base is read by invoking gh, so it is not a
// record this command can offer however useful the comparison would be.
func validateOfflineSource(from string) error {
	if from == "" || stack.Permits(stack.OfflineSources, stack.Source(from)) {
		return nil
	}
	if stack.Permits(stack.ReadableSources, stack.Source(from)) {
		return fmt.Errorf("g2g status cannot read structure from %q · it takes g2g or graphite, because reading a pull request base means invoking gh and this command answers without a network · g2g github status --from %s does read it", from, from)
	}
	return fmt.Errorf("unknown source %q · g2g status reads g2g or graphite", from)
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
	}, "g2g status")
	if err != nil {
		return err
	}
	return writeStackView(cmd.OutOrStdout(), structureNote(sourceGraphView(snapshot), snapshot), p)
}

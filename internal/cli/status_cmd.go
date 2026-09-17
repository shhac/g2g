package cli

import (
	"errors"

	"github.com/shhac/g2g/internal/link"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
	"github.com/spf13/cobra"
)

func newStatus(service link.Service, completions stack.Completions, presentation Presentation) *cobra.Command {
	var selection stackOptions
	cmd := &cobra.Command{Use: "status", GroupID: groupPublish, Short: "Inspect a stack, its pull requests, and native GitHub membership (read-only)", Args: cobra.NoArgs}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		presentation := presentation.resolve(cmd)
		if err := selection.validate(); err != nil {
			return err
		}
		ctx, cancel := newBudgets(cmd).discovery(commandContext(cmd.Context(), cmd, "status", "read_only", selection.branch, selection.trunk))
		defer cancel()
		plan, err := service.Plan(ctx, selection.Selection())
		if err != nil {
			// "Nothing is stacked here" is an answer to what status was asked,
			// not a failure to answer it. Refusing meant the read-only triage
			// entry point exited non-zero on a repository that is simply not
			// stacked yet — while graph rendered the very same fact and exited
			// zero. Only this command renders it; anything that mutates still
			// has nothing to act on and still refuses.
			var undescribed stack.Undescribed
			if errors.As(err, &undescribed) {
				return writeUnstacked(cmd.OutOrStdout(), undescribed, presentation)
			}
			return err
		}
		return writeStatus(cmd.OutOrStdout(), plan, presentation)
	}
	selection.register(cmd, completions, stack.ReadableSources, "local branch to inspect (defaults to current branch)", "trunk to use as the base")
	// status reads, so it defaults to the whole stack: ancestors, descendants,
	// and where the target sits between them. It stops short of all, because a
	// repository's other trunks are not what someone triaging this one asked
	// about.
	selection.registerScope(cmd, shape.Scopes, stack.ScopeStack, scopeUsage("show", shape.Scopes))
	return cmd
}

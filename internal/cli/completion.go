package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/graph"
)

func completionCallback(resolve func(context.Context, string) ([]string, error)) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, _ []string, prefix string) ([]string, cobra.ShellCompDirective) {
		ctx, cancel := context.WithTimeout(context.Background(), completionTimeout)
		defer cancel()
		branches, err := resolve(ctx, prefix)
		if err != nil {
			return nil, cobra.ShellCompDirectiveError | cobra.ShellCompDirectiveNoFileComp
		}
		return branches, cobra.ShellCompDirectiveNoFileComp
	}
}

func localBranchCompletions(service graph.Service) func(context.Context, string) ([]string, error) {
	return func(ctx context.Context, prefix string) ([]string, error) {
		if service.Git == nil {
			return nil, nil
		}
		return service.Git.LocalBranches(ctx)
	}
}

func newCompletion(root *cobra.Command) *cobra.Command {
	// Hidden, not removed. This is run once by a shell rc or by the Homebrew
	// formula's generate_completions_from_executable, and never typed while
	// working on a stack, so it is noise in a help listing whose other entries
	// are all things a person runs. Hidden affects the listing alone: the
	// command still executes, which is what the formula depends on.
	cmd := &cobra.Command{
		Use:    "completion [bash|zsh|fish]",
		Short:  "Generate shell completion scripts",
		Args:   cobra.ExactArgs(1),
		Hidden: true,
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		switch args[0] {
		case "bash":
			return root.GenBashCompletion(cmd.OutOrStdout())
		case "zsh":
			return root.GenZshCompletion(cmd.OutOrStdout())
		case "fish":
			return root.GenFishCompletion(cmd.OutOrStdout(), true)
		default:
			return fmt.Errorf("unsupported shell %q (want bash, zsh, or fish)", args[0])
		}
	}
	return cmd
}

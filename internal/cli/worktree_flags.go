package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/worktree"
)

// registerWorktrees declares --worktrees on every command that moves
// branches, so restack, pull and land spell and explain it the same way.
func registerWorktrees(cmd *cobra.Command, into *string) {
	cmd.Flags().StringVar(into, "worktrees", string(worktree.Follow),
		"a branch another worktree has checked out: follow moves it when that worktree is clean and brings its files along, leaving one with changes; skip leaves every such branch; refuse moves nothing")
	_ = cmd.RegisterFlagCompletionFunc("worktrees", completionCallback(func(context.Context, string) ([]string, error) {
		values := make([]string, 0, len(worktree.Policies))
		for _, policy := range worktree.Policies {
			values = append(values, string(policy))
		}
		return values, nil
	}))
}

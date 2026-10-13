package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/worktree"
)

// registerWorktrees declares --worktrees on every command that moves
// branches, so restack, pull and land spell and explain it the same way, and
// each holds the parsed policy rather than a string it checked somewhere
// else and converts again where it is used.
func registerWorktrees(cmd *cobra.Command, into *worktree.Policy) {
	*into = worktree.Follow
	cmd.Flags().Var(policyValue{into}, "worktrees",
		"a branch another worktree has checked out: follow moves it when that worktree is clean and brings its files along, leaving one with changes; skip leaves every such branch; refuse moves nothing")
	_ = cmd.RegisterFlagCompletionFunc("worktrees", completionCallback(func(context.Context, string) ([]string, error) {
		values := make([]string, 0, len(worktree.Policies))
		for _, policy := range worktree.Policies {
			values = append(values, string(policy))
		}
		return values, nil
	}))
}

// policyValue parses --worktrees when the flag is read, so a value nobody
// accepts is refused before anything else happens.
type policyValue struct{ policy *worktree.Policy }

func (v policyValue) String() string {
	if v.policy == nil {
		return ""
	}
	return string(*v.policy)
}

func (v policyValue) Set(value string) error {
	parsed, err := worktree.Parse(value)
	if err != nil {
		return err
	}
	*v.policy = parsed
	return nil
}

func (policyValue) Type() string { return "policy" }

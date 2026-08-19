package git

import (
	"context"
	"strings"
)

// CheckedOutElsewhere maps each branch checked out in another worktree to the
// worktree holding it. The current worktree is excluded: rewriting the branch
// you are standing on is ordinary, and the engines handle it.
//
// A rewrite does not need to check a branch out to move it, so nothing stopped
// one from moving a branch another worktree had. Git updates the ref, that
// worktree's index and working tree still describe the old commit, and its next
// git status reports staged changes nobody made.
func (c Client) CheckedOutElsewhere(ctx context.Context) (map[string]string, error) {
	output, err := c.run(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	current, err := c.run(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	here := strings.TrimSpace(string(current))

	elsewhere := map[string]string{}
	var path string
	for _, line := range outputLines(output) {
		switch {
		case strings.HasPrefix(line, "worktree "):
			path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch "):
			// A detached worktree emits no branch line, so it cannot conflict:
			// there is no ref for a rewrite to move underneath it.
			branch := strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
			if path != "" && path != here && branch != "" {
				elsewhere[branch] = path
			}
		}
	}
	return elsewhere, nil
}

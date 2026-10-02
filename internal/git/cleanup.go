package git

import (
	"context"
	"fmt"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/subprocess"
)

// DeleteBranchAt removes only the ref whose content the caller assessed.
// Native branch -D has no expected-tip argument; update-ref supplies that
// lease, while the worktree check supplies branch -D's checkout protection.
func (c Client) DeleteBranchAt(ctx context.Context, branch, tip string) error {
	for _, value := range []string{branch, tip} {
		if err := safeRef(value); err != nil {
			return err
		}
	}
	if tip == "" {
		return fmt.Errorf("the assessed branch tip is required")
	}
	holders, err := c.BranchHolders(ctx)
	if err != nil {
		return err
	}
	if path, held := holders[branch]; held {
		return fmt.Errorf("%s is checked out in %s", branch, path)
	}
	if _, err := c.run(ctx, "update-ref", "--no-deref", "-d", "refs/heads/"+branch, tip); err != nil {
		return err
	}
	// Ref deletion is complete. Removing optional branch configuration is
	// best-effort, as it is in Git's branch deletion; a missing section is normal.
	if _, err := c.run(ctx, "config", "--remove-section", "branch."+branch); err != nil {
		if code, ok := subprocess.ExitCode(err); !ok || code != 128 {
			diagnostic.Warn(ctx, "prune.branch_config", "local branch deleted; its branch configuration could not be removed")
		}
	}
	return nil
}

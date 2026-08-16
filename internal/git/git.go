// Package git provides the small Git boundary used by gt2gh.
package git

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/shhac/gt2gh/internal/diagnostic"
	"github.com/shhac/gt2gh/internal/subprocess"
)

// Client runs the narrow Git command set required by gt2gh.
type Client struct {
	Runner subprocess.Runner
}

var errCounts = errors.New("parse git rev-list --left-right --count output")

// forkPointPrefix keeps every recorded fork point reachable.
//
// A fork point is the one object a restack cannot do without, and it becomes
// unreachable exactly when it matters most: after a merged parent's branch is
// deleted. Naming it with a ref stops garbage collection taking it.
const forkPointPrefix = "refs/g2g/forkpoints/"

// isolatedRemotePrefix namespaces the refs gt2gh fetches for itself.
//
// gt2gh must never move the user's remote-tracking refs. Doing so is not just
// cosmetic noise in "git status": a bare --force-with-lease uses the
// remote-tracking ref as its lease baseline, so refreshing it behind the
// user's back silently disarms the one check that stops a force push
// destroying work that landed in the meantime.
const isolatedRemotePrefix = "refs/g2g/remotes/"

// CommonDir returns the absolute Git common directory, which linked worktrees
// share. --path-format=absolute is required: the bare form is resolved against
// the current working directory, so it answers ".git" from the repository root
// and "../../.git" from a subdirectory.
func (c Client) CommonDir(ctx context.Context) (string, error) {
	output, err := c.run(ctx, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	dir := strings.TrimSpace(string(output))
	if dir == "" {
		return "", fmt.Errorf("git rev-parse returned no common directory")
	}
	return dir, nil
}

// safeRef rejects the ref names that cannot be passed to Git as a positional
// argument without being read as an option.
func safeRef(ref string) error {
	return subprocess.CheckArgument("git", "branch name", ref)
}

func (c Client) Clean(ctx context.Context) error {
	output, err := c.run(ctx, "status", "--porcelain")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(output)) != "" {
		return fmt.Errorf("working tree is not clean; commit or stash changes before --apply")
	}
	return nil
}

// Remote validates that name resolves to a configured remote without making
// a network request.
func (c Client) Remote(ctx context.Context, name string) error {
	if err := subprocess.CheckArgument("git", "remote name", name); err != nil {
		return err
	}
	_, err := c.run(ctx, "remote", "get-url", name)
	return err
}

// absentOnRemote is the lease value asserting a branch does not exist on the
// remote yet. git rejects the push if one has appeared since.
const absentOnRemote = "0000000000000000000000000000000000000000"

// Lease is one branch plus the remote tip the plan observed for it. Expected
// is empty when the branch was not on the remote at all.
type Lease struct {
	Branch   string
	Expected string
}

func (c Client) run(ctx context.Context, args ...string) ([]byte, error) {
	if c.Runner == nil {
		return nil, fmt.Errorf("Git runner is not configured")
	}
	output, err := c.Runner.Run(ctx, "git", args...)
	if err != nil {
		// Git says why it failed on its own output, and discarding it leaves
		// an exit status where a reason should be — which is the difference
		// between "a rebase failed" and "these files conflict".
		if reason := diagnostic.BoundedOutput(output); reason != "" {
			return nil, fmt.Errorf("git %s failed: %w: %s", strings.Join(args, " "), err, reason)
		}
		return nil, fmt.Errorf("git %s failed: %w", strings.Join(args, " "), err)
	}
	return output, nil
}

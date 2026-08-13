// Package git provides the small Git boundary used by gt2gh.
package git

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/shhac/gt2gh/internal/subprocess"
)

// Client runs the narrow Git command set required by gt2gh.
type Client struct {
	Runner subprocess.Runner
}

func (c Client) CurrentBranch(ctx context.Context) (string, error) {
	output, err := c.run(ctx, "branch", "--show-current")
	if err != nil {
		return "", err
	}
	branch := strings.TrimSpace(string(output))
	if branch == "" {
		return "", fmt.Errorf("HEAD is detached; pass --branch to select a local Graphite branch")
	}
	return branch, nil
}

func (c Client) LocalBranches(ctx context.Context) ([]string, error) {
	output, err := c.run(ctx, "branch", "--format=%(refname:short)")
	if err != nil {
		return nil, err
	}
	var branches []string
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			branches = append(branches, line)
		}
	}
	sort.Strings(branches)
	return branches, nil
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
	if name == "" || strings.HasPrefix(name, "-") {
		return fmt.Errorf("remote name must be nonempty and must not start with '-'")
	}
	_, err := c.run(ctx, "remote", "get-url", name)
	return err
}

// PushAtomic force-with-lease pushes every branch as one atomic operation. It
// deliberately has no fallback to a weaker push mode.
func (c Client) PushAtomic(ctx context.Context, remote string, branches []string) error {
	if err := c.Remote(ctx, remote); err != nil {
		return err
	}
	if len(branches) == 0 {
		return fmt.Errorf("no branches selected for push")
	}
	args := append([]string{"push", "--atomic", "--force-with-lease", remote}, branches...)
	_, err := c.run(ctx, args...)
	return err
}

func (c Client) run(ctx context.Context, args ...string) ([]byte, error) {
	if c.Runner == nil {
		return nil, fmt.Errorf("Git runner is not configured")
	}
	output, err := c.Runner.Run(ctx, "git", args...)
	if err != nil {
		return nil, fmt.Errorf("git %s failed: %w", strings.Join(args, " "), err)
	}
	return output, nil
}

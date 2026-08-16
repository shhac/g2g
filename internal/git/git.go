// Package git provides the small Git boundary used by gt2gh.
package git

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
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

// AncestorBranches returns every other local branch whose tip is reachable
// from target, which is exactly the set of candidate parents for target.
//
// This is the primitive g2g-owned graphs are inferred from. It needs no
// network and works for branches that were never pushed, which is the whole
// case pull request bases cannot describe.
func (c Client) AncestorBranches(ctx context.Context, target string) ([]string, error) {
	if err := safeRef(target); err != nil {
		return nil, err
	}
	output, err := c.run(ctx, "for-each-ref", "--format=%(refname:short)", "--merged", target, "refs/heads/")
	if err != nil {
		return nil, err
	}
	branches := make([]string, 0)
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		// for-each-ref includes the target itself; a candidate parent set that
		// contains the target would let a branch be recorded as its own parent.
		if line = strings.TrimSpace(line); line != "" && line != target {
			branches = append(branches, line)
		}
	}
	sort.Strings(branches)
	return branches, nil
}

// Divergence counts the commits each of two branches has that the other does
// not, measured from their merge base.
//
// This is what finds a parent once the obvious answer has gone. A branch that
// forked from a trunk stops being reachable from it the moment the trunk moves
// on, so ancestry alone reports nothing at all — but the fork point is still
// there, and behind counts exactly the commits the target added since it.
//
// One invocation answers both directions: ahead of zero means other is an
// ancestor, behind of zero means it is a descendant and therefore never a
// candidate parent.
func (c Client) Divergence(ctx context.Context, other, target string) (ahead, behind int, err error) {
	if err := safeRef(other); err != nil {
		return 0, 0, err
	}
	if err := safeRef(target); err != nil {
		return 0, 0, err
	}
	output, err := c.run(ctx, "rev-list", "--left-right", "--count", other+"..."+target)
	if err != nil {
		return 0, 0, err
	}
	fields := strings.Fields(string(output))
	if len(fields) != 2 {
		return 0, 0, errCounts
	}
	if ahead, err = strconv.Atoi(fields[0]); err != nil {
		return 0, 0, errCounts
	}
	if behind, err = strconv.Atoi(fields[1]); err != nil {
		return 0, 0, errCounts
	}
	return ahead, behind, nil
}

var errCounts = errors.New("parse git rev-list --left-right --count output")

// IsAncestor reports whether ancestor's tip is reachable from descendant.
//
// git signals the negative answer with exit status 1, which the runner
// reports as an error like any other failure. Treating that as a failure
// would turn every ordinary "no" into a broken command, so exit 1 alone is
// translated back into a false answer and every other status stays an error.
func (c Client) IsAncestor(ctx context.Context, ancestor, descendant string) (bool, error) {
	if err := safeRef(ancestor); err != nil {
		return false, err
	}
	if err := safeRef(descendant); err != nil {
		return false, err
	}
	_, err := c.run(ctx, "merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

// isolatedRemotePrefix namespaces the refs gt2gh fetches for itself.
//
// gt2gh must never move the user's remote-tracking refs. Doing so is not just
// cosmetic noise in "git status": a bare --force-with-lease uses the
// remote-tracking ref as its lease baseline, so refreshing it behind the
// user's back silently disarms the one check that stops a force push
// destroying work that landed in the meantime.
const isolatedRemotePrefix = "refs/g2g/remotes/"

// RemoteTips reads the remote's current branch tips without writing anything
// at all — no refs, no objects, no FETCH_HEAD.
//
// This is how a preview learns that a trunk has moved without touching the
// repository. Branches with no remote counterpart are simply absent from the
// result rather than being an error, because "not pushed yet" is ordinary.
func (c Client) RemoteTips(ctx context.Context, remote string, branches []string) (map[string]string, error) {
	if err := c.Remote(ctx, remote); err != nil {
		return nil, err
	}
	args := []string{"ls-remote", "--heads", remote}
	for _, branch := range branches {
		if err := safeRef(branch); err != nil {
			return nil, err
		}
		args = append(args, "refs/heads/"+branch)
	}
	output, err := c.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	tips := make(map[string]string, len(branches))
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		object, ref, found := strings.Cut(strings.TrimSpace(line), "\t")
		if !found {
			continue
		}
		tips[strings.TrimPrefix(strings.TrimSpace(ref), "refs/heads/")] = object
	}
	return tips, nil
}

// FetchIsolated downloads the named branches into gt2gh's own ref namespace,
// leaving every ref the user relies on exactly where it was.
//
// Both flags are load-bearing. --refmap= suppresses git's opportunistic update
// of the remote-tracking ref that the configured refspec would otherwise
// match, which a private destination refspec alone does not prevent.
// --no-write-fetch-head keeps FETCH_HEAD intact for whatever the user was
// doing.
func (c Client) FetchIsolated(ctx context.Context, remote string, branches []string) error {
	if err := c.Remote(ctx, remote); err != nil {
		return err
	}
	if len(branches) == 0 {
		return fmt.Errorf("no branches selected for fetch")
	}
	args := []string{"fetch", "--refmap=", "--no-write-fetch-head", "--no-tags", remote}
	for _, branch := range branches {
		if err := safeRef(branch); err != nil {
			return err
		}
		args = append(args, "refs/heads/"+branch+":"+IsolatedRef(remote, branch))
	}
	_, err := c.run(ctx, args...)
	return err
}

// IsolatedRef names the ref FetchIsolated writes for one remote branch. It is
// a normal commit-ish, so it can be used as a rebase target directly.
func IsolatedRef(remote, branch string) string {
	return isolatedRemotePrefix + remote + "/" + branch
}

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
	if ref == "" {
		return fmt.Errorf("branch name must not be empty")
	}
	if strings.HasPrefix(ref, "-") {
		return fmt.Errorf("branch name %q cannot be passed safely to git", ref)
	}
	return nil
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

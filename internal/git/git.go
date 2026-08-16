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

	"github.com/shhac/gt2gh/internal/diagnostic"
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

// Resolve returns the object id a revision names. It is how a fork point
// recorded as text is checked against the repository that has to contain it.
func (c Client) Resolve(ctx context.Context, revision string) (string, error) {
	if err := safeRef(revision); err != nil {
		return "", err
	}
	// ^{commit} makes an object that exists but is not a commit an error here
	// rather than a confusing failure inside a later rebase.
	output, err := c.run(ctx, "rev-parse", "--verify", "--quiet", revision+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("revision %q is not a commit in this repository", revision)
	}
	resolved := strings.TrimSpace(string(output))
	if resolved == "" {
		return "", fmt.Errorf("revision %q is not a commit in this repository", revision)
	}
	return resolved, nil
}

// forkPointPrefix keeps every recorded fork point reachable.
//
// A fork point is the one object a restack cannot do without, and it becomes
// unreachable exactly when it matters most: after a merged parent's branch is
// deleted. Naming it with a ref stops garbage collection taking it.
const forkPointPrefix = "refs/g2g/forkpoints/"

// PinForkPoint records a ref for a fork point so the object survives gc.
func (c Client) PinForkPoint(ctx context.Context, branch, object string) error {
	if err := safeRef(branch); err != nil {
		return err
	}
	if err := safeRef(object); err != nil {
		return err
	}
	_, err := c.run(ctx, "update-ref", forkPointPrefix+branch, object)
	return err
}

// UnpinForkPoint drops the ref for a branch gt2gh no longer records.
func (c Client) UnpinForkPoint(ctx context.Context, branch string) error {
	if err := safeRef(branch); err != nil {
		return err
	}
	// -d on a ref that is already gone is an error, and an untrack of a branch
	// that never had a pin is ordinary, so a missing ref is not a failure.
	if _, err := c.run(ctx, "update-ref", "-d", forkPointPrefix+branch); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil
		}
		return err
	}
	return nil
}

// CherryDropped returns the commits in upstream..head whose content is not
// already present in upstream.
//
// This separates a commit a rewritten parent genuinely dropped from one it
// merely rewrote. The distinction decides whether a child may absorb what its
// parent no longer has: absorbing a rewritten commit would give the child a
// stale duplicate of work the parent still carries under a new object id.
func (c Client) CherryDropped(ctx context.Context, upstream, head string) ([]string, error) {
	kept, _, err := c.Cherry(ctx, upstream, head, "")
	return kept, err
}

// Cherry compares the commits in limit..head against upstream by content,
// returning those with no equivalent there and those with one.
//
// limit is optional and narrows the comparison to a branch's own commits,
// which is what distinguishes "this branch has nothing left to contribute"
// from "some commit somewhere below it is already upstream".
func (c Client) Cherry(ctx context.Context, upstream, head, limit string) (absent, present []string, err error) {
	if err := safeRef(upstream); err != nil {
		return nil, nil, err
	}
	if err := safeRef(head); err != nil {
		return nil, nil, err
	}
	args := []string{"cherry", upstream, head}
	if limit != "" {
		if err := safeRef(limit); err != nil {
			return nil, nil, err
		}
		args = append(args, limit)
	}
	output, err := c.run(ctx, args...)
	if err != nil {
		return nil, nil, err
	}
	absent, present = make([]string, 0), make([]string, 0)
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		mark, object, found := strings.Cut(strings.TrimSpace(line), " ")
		if !found {
			continue
		}
		// A leading + means no equivalent content upstream; a leading - means
		// the same content is already there under another object id.
		if mark == "+" {
			absent = append(absent, object)
			continue
		}
		present = append(present, object)
	}
	return absent, present, nil
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

// absentOnRemote is the lease value asserting a branch does not exist on the
// remote yet. git rejects the push if one has appeared since.
const absentOnRemote = "0000000000000000000000000000000000000000"

// Lease is one branch plus the remote tip the plan observed for it. Expected
// is empty when the branch was not on the remote at all.
type Lease struct {
	Branch   string
	Expected string
}

// Argument renders the pinned lease flag for this branch.
func (l Lease) Argument() string {
	expected := l.Expected
	if expected == "" {
		expected = absentOnRemote
	}
	return "--force-with-lease=refs/heads/" + l.Branch + ":" + expected
}

// PushAtomic publishes every branch as one atomic operation, each protected by
// a lease naming the exact remote tip the plan was built against.
//
// The lease is pinned rather than left bare on purpose. A bare
// --force-with-lease takes its baseline from the remote-tracking ref, so what
// it protects depends on when the user last happened to run git fetch — and
// any fetch performed in between refreshes the baseline to the very commit the
// check exists to defend. Naming the observed tip makes the push assert
// exactly what the preview showed, which is the same contract revalidation
// already provides for everything else.
//
// There is deliberately no fallback to a weaker push mode.
func (c Client) PushAtomic(ctx context.Context, remote string, leases []Lease) error {
	if err := c.Remote(ctx, remote); err != nil {
		return err
	}
	if len(leases) == 0 {
		return fmt.Errorf("no branches selected for push")
	}
	args := []string{"push", "--atomic"}
	branches := make([]string, 0, len(leases))
	for _, lease := range leases {
		if err := safeRef(lease.Branch); err != nil {
			return err
		}
		args = append(args, lease.Argument())
		branches = append(branches, lease.Branch)
	}
	args = append(args, remote)
	args = append(args, branches...)
	_, err := c.run(ctx, args...)
	return err
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

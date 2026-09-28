// Read-only questions about how branches relate.
//
// Nothing in this file writes a ref or reaches the network. That is what makes
// it the half of the client a caller can invoke freely, and it is why it is
// separated from the half that does.
package git

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/shhac/g2g/internal/subprocess"
)

func (c Client) CurrentBranch(ctx context.Context) (string, error) {
	output, err := c.run(ctx, "branch", "--show-current")
	if err != nil {
		return "", err
	}
	branch := strings.TrimSpace(string(output))
	if branch == "" {
		return "", fmt.Errorf("HEAD is detached; pass --branch to select a local branch")
	}
	return branch, nil
}
func (c Client) LocalBranches(ctx context.Context) ([]string, error) {
	output, err := c.run(ctx, "branch", "--format=%(refname:short)")
	if err != nil {
		return nil, err
	}
	branches := append([]string(nil), outputLines(output)...)
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
	for _, line := range outputLines(output) {
		// for-each-ref includes the target itself; a candidate parent set that
		// contains the target would let a branch be recorded as its own parent.
		if line != target {
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
	if code, exited := subprocess.ExitCode(err); exited && code == 1 {
		return false, nil
	}
	return false, err
}

// MergeBase reports the commit two revisions last had in common.
//
// Histories that share nothing have no answer, and git says so with an empty
// output and exit status 1. That is reported as an error rather than an empty
// commit, because every caller wants the commit to replay from and there is
// no such commit to hand them.
func (c Client) MergeBase(ctx context.Context, one, other string) (string, error) {
	if err := safeRef(one); err != nil {
		return "", err
	}
	if err := safeRef(other); err != nil {
		return "", err
	}
	output, err := c.run(ctx, "merge-base", one, other)
	if err != nil {
		return "", fmt.Errorf("%s and %s share no history: %w", one, other, err)
	}
	base := strings.TrimSpace(string(output))
	if base == "" {
		return "", fmt.Errorf("%s and %s share no history", one, other)
	}
	return base, nil
}

// ResolveAll resolves many revisions in one process.
//
// Resolving them one at a time cost a process each, and a status on a
// fourteen-branch stack asked twenty-nine times: every branch, and the commit
// every pull request is on. git rev-parse takes them all at once and prints one
// object id per line.
//
// It reports what resolved rather than failing when something does not, because
// the caller's question is whether this repository has the commit at all — a
// pull request may be on one nobody here has fetched. The batch cannot say
// which one failed, so the rare mixed case is asked again one at a time, which
// is exactly what this replaced and no worse than it.
func (c Client) ResolveAll(ctx context.Context, revisions []string) (map[string]string, error) {
	resolved := make(map[string]string, len(revisions))
	asking := make([]string, 0, len(revisions))
	for _, revision := range revisions {
		if _, repeated := resolved[revision]; repeated || revision == "" {
			continue
		}
		if err := safeRef(revision); err != nil {
			return nil, err
		}
		resolved[revision] = ""
		asking = append(asking, revision)
	}
	if len(asking) == 0 {
		return map[string]string{}, nil
	}

	args := make([]string, 0, len(asking)+1)
	args = append(args, "rev-parse")
	for _, revision := range asking {
		args = append(args, revision+"^{commit}")
	}
	lines := strings.Fields(string(mustNot(c.run(ctx, args...))))
	if len(lines) == len(asking) && allObjectIDs(lines) {
		for index, revision := range asking {
			resolved[revision] = lines[index]
		}
		return present(resolved), nil
	}
	for _, revision := range asking {
		if oid, err := c.Resolve(ctx, revision); err == nil {
			resolved[revision] = oid
		}
	}
	return present(resolved), nil
}

// mustNot drops the error and keeps the output. A batch that failed is not an
// answer about any particular revision, and the caller falls back to asking
// about them individually — where the error is raised properly.
func mustNot(output []byte, _ error) []byte { return output }

// allObjectIDs guards the fast path. A failed batch still prints the revisions
// it could resolve, mixed with the ones it could not and with git's complaint
// about them, so the answer is only trusted when every line is an object id.
func allObjectIDs(lines []string) bool {
	for _, line := range lines {
		if len(line) < 40 {
			return false
		}
		for _, char := range line {
			if !strings.ContainsRune("0123456789abcdef", char) {
				return false
			}
		}
	}
	return true
}

func present(resolved map[string]string) map[string]string {
	for revision, oid := range resolved {
		if oid == "" {
			delete(resolved, revision)
		}
	}
	return resolved
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

// Read-only questions about how branches relate.
//
// Nothing in this file writes a ref or reaches the network. That is what makes
// it the half of the client a caller can invoke freely, and it is why it is
// separated from the half that does.
package git

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

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

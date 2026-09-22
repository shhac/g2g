package git

import (
	"context"
	"fmt"
	"strings"
)

// Reshaping: moving, renaming and reading a branch whose place in a stack is
// about to change.
//
// None of these rewrites a commit. A fast-forward moves a ref to a commit that
// already contains it, and a rename moves the ref and its configuration; both
// are why delete, fold and rename are not restack, which remains the only
// command that replays history.

// Commit is one commit named by its full object id, with its subject so a
// preview can say what it is rather than only that it exists.
type Commit struct {
	ID      string
	Subject string
}

// Unpublished lists the commits in since..branch that no remote-tracking ref
// reaches, newest first.
//
// It is a local read: it asks what this clone last saw of every remote and
// fetches nothing. That answers "is this anywhere else" as well as a local
// read can — a commit a remote holds but this clone never fetched reads as
// unpublished, which errs toward warning.
func (c Client) Unpublished(ctx context.Context, branch, since string) ([]Commit, error) {
	if err := safeRef(branch); err != nil {
		return nil, err
	}
	if err := safeRef(since); err != nil {
		return nil, err
	}
	output, err := c.run(ctx, "log", "--format=%H%x09%s", since+".."+branch, "--not", "--remotes")
	if err != nil {
		return nil, err
	}
	commits := make([]Commit, 0)
	for _, line := range outputLines(output) {
		id, subject, _ := strings.Cut(line, "\t")
		commits = append(commits, Commit{ID: id, Subject: subject})
	}
	return commits, nil
}

// RemoteTracking names the remote-tracking refs that carry branch's name, as
// "remote/branch". A local read: it reports what the last fetch recorded,
// which is what a person sees in git branch -r.
func (c Client) RemoteTracking(ctx context.Context, branch string) ([]string, error) {
	if err := safeRef(branch); err != nil {
		return nil, err
	}
	output, err := c.run(ctx, "for-each-ref", "--format=%(refname)", "refs/remotes/*/"+branch)
	if err != nil {
		return nil, err
	}
	refs := make([]string, 0)
	for _, ref := range outputLines(output) {
		// for-each-ref also matches a pattern up to a slash, so a name that is
		// a prefix of another's path must match exactly or not at all.
		if strings.HasSuffix(ref, "/"+branch) {
			refs = append(refs, strings.TrimPrefix(ref, "refs/remotes/"))
		}
	}
	return refs, nil
}

// MoveBranch points branch at to, only if it still points at from.
//
// The old value is the lease: the move is planned against a tip, and a branch
// that moved since is refused by update-ref itself rather than overwritten.
// It does not touch the checkout; a caller moving the branch it stands on
// brings the tree along with SwitchTree.
func (c Client) MoveBranch(ctx context.Context, branch, from, to string) error {
	for _, value := range []string{branch, from, to} {
		if err := safeRef(value); err != nil {
			return err
		}
	}
	_, err := c.run(ctx, "update-ref", "-m", "g2g: move "+branch, "refs/heads/"+branch, to, from)
	return err
}

// RenameBranch renames a local branch with git branch -m, which carries its
// reflog and configuration and moves HEAD in every worktree that has it
// checked out. It refuses a name that is already taken rather than forcing.
func (c Client) RenameBranch(ctx context.Context, from, to string) error {
	if err := safeRef(from); err != nil {
		return err
	}
	if err := safeRef(to); err != nil {
		return err
	}
	if from == to {
		return fmt.Errorf("%s already has that name", from)
	}
	_, err := c.run(ctx, "branch", "-m", from, to)
	return err
}

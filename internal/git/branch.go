package git

import (
	"context"
	"fmt"
	"strings"
)

// Making a branch and moving between branches.
//
// These are the only calls here that start a branch or commit to one. They
// exist for `create` and the navigation commands, which act on the checkout
// the user is standing in — the one thing every other command here goes out of
// its way to leave alone.

// CheckBranchName refuses a name Git would not accept for a new branch.
//
// check-ref-format --branch also expands the previous-branch shorthand, so
// "@{-1}" passes and prints the branch it stands for. A name that comes back
// different is refused too: creating a branch the user did not name is a guess.
func (c Client) CheckBranchName(ctx context.Context, name string) error {
	if err := safeRef(name); err != nil {
		return err
	}
	output, err := c.run(ctx, "check-ref-format", "--branch", name)
	if err != nil {
		return fmt.Errorf("%q is not a valid branch name", name)
	}
	if expanded := strings.TrimSpace(string(output)); expanded != name {
		return fmt.Errorf("%q names another branch (%s) rather than a new one", name, expanded)
	}
	return nil
}

// CreateBranch starts a branch at start and checks it out.
//
// git switch -c is what brings the index and working tree along, and it
// refuses rather than overwriting a local change the new start point would
// clobber.
func (c Client) CreateBranch(ctx context.Context, name, start string) error {
	if err := safeRef(name); err != nil {
		return err
	}
	if err := safeRef(start); err != nil {
		return err
	}
	_, err := c.run(ctx, "switch", "-c", name, start)
	return err
}

// SwitchExisting moves the checkout to a branch that already exists here.
//
// --no-guess is the difference from SwitchBranch. Without it, git switch given
// a name with no local branch but a remote-tracking one of the same name
// quietly creates the branch — so a destination read from a record that has
// gone stale would be recreated from the remote rather than refused.
func (c Client) SwitchExisting(ctx context.Context, branch string) error {
	if err := safeRef(branch); err != nil {
		return err
	}
	_, err := c.run(ctx, "switch", "--no-guess", branch)
	return err
}

// StagedPaths lists what the next commit would hold, relative to HEAD.
func (c Client) StagedPaths(ctx context.Context) ([]string, error) {
	output, err := c.run(ctx, "diff", "--cached", "--name-only")
	if err != nil {
		return nil, err
	}
	return outputLines(output), nil
}

// Commit records what is staged, with the given message.
//
// The message goes as one --message= argument so that one starting with a
// dash is a message and never an option. Hooks run as they would for the
// user's own commit, because this is their commit.
func (c Client) Commit(ctx context.Context, message string) error {
	if strings.TrimSpace(message) == "" {
		return fmt.Errorf("a commit needs a message")
	}
	_, err := c.run(ctx, "commit", "--quiet", "--message="+message)
	return err
}

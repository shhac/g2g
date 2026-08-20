package git

import (
	"context"
	"fmt"
)

// Moving a branch ref, and bringing the checkout with it.
//
// This is a different concept from the two rewrite engines next door, and a
// different audience: internal/sync declares FastForward and ResetBranch on its
// Git interface and never touches replay or rebase. followCheckout's own
// comment argues the move is one concept — it was found in four places before
// it lived in one — which is the same argument for it being one file.

// UpdateBranch points a branch at an object. It is how an aborted restack
// restores a branch git has already forgotten about, because git only rolls
// back the single rebase invocation it was running.
func (c Client) UpdateBranch(ctx context.Context, branch, object string) error {
	if err := safeRef(branch); err != nil {
		return err
	}
	if err := safeRef(object); err != nil {
		return err
	}
	_, err := c.run(ctx, "update-ref", "refs/heads/"+branch, object)
	return err
}

// FastForward advances a branch to a commit that already contains it.
//
// It refuses anything else. A trunk that has diverged — local commits, or an
// upstream rewrite — is not something to merge or reset behind the user's
// back, and the difference between "you are behind" and "you have diverged" is
// exactly what they need to be told.
func (c Client) FastForward(ctx context.Context, branch, to string) error {
	if err := safeRef(branch); err != nil {
		return err
	}
	if err := safeRef(to); err != nil {
		return err
	}
	current, err := c.Resolve(ctx, branch)
	if err != nil {
		return err
	}
	target, err := c.Resolve(ctx, to)
	if err != nil {
		return err
	}
	if current == target {
		return nil
	}
	contains, err := c.IsAncestor(ctx, branch, to)
	if err != nil {
		return err
	}
	if !contains {
		return fmt.Errorf("%s and %s have each moved where the other has not, so %s cannot be fast-forwarded; reconcile it yourself", branch, to, branch)
	}
	if err := c.UpdateBranch(ctx, branch, target); err != nil {
		return err
	}
	return c.followCheckout(ctx, branch, current, target)
}

// ResetBranch points a branch at a commit that does not contain it, bringing
// the checkout with it.
//
// It is the deliberate counterpart to FastForward. Taking a published version
// that supersedes the local one — somebody rebased your branch and pushed it —
// is neither a merge nor a fast-forward, so FastForward would refuse it and
// calling it one would be a lie. Naming it separately is what keeps "you are
// behind" and "this replaces what you have" from becoming the same call.
func (c Client) ResetBranch(ctx context.Context, branch, to string) error {
	if err := safeRef(branch); err != nil {
		return err
	}
	if err := safeRef(to); err != nil {
		return err
	}
	current, err := c.Resolve(ctx, branch)
	if err != nil {
		return err
	}
	target, err := c.Resolve(ctx, to)
	if err != nil {
		return err
	}
	if current == target {
		return nil
	}
	if err := c.UpdateBranch(ctx, branch, target); err != nil {
		return err
	}
	return c.followCheckout(ctx, branch, current, target)
}

// followCheckout brings the index and working tree with a branch whose ref has
// just moved, when that branch is the one checked out here.
//
// Moving a ref does not touch the working tree, so advancing the branch you are
// standing on leaves the tree describing the commit before — reported by git as
// changes nobody made, and enough to block the next git switch. This has been
// found four times in four places now, so it lives with the move rather than
// with each caller that performs one.
//
// A detached HEAD has no branch to follow and CurrentBranch says so by failing,
// which is not a reason to fail the move that already succeeded.
func (c Client) followCheckout(ctx context.Context, branch, from, to string) error {
	head, err := c.CurrentBranch(ctx)
	if err != nil || head != branch || from == to {
		return nil
	}
	return c.SwitchTree(ctx, from, to)
}

// SwitchTree updates the index and working tree from one commit to another,
// without moving any ref.
//
// A replay moves refs without touching the working tree, so a user standing on
// a rewritten branch is left with an index and tree describing the old commit —
// which git reports as changes they never made, and which blocks the next
// git switch with "local changes would be overwritten".
//
// "git reset --keep HEAD" was the previous answer and could not work: --keep
// updates the files that differ between the target and HEAD, and by the time it
// runs the ref has already moved, so the two are the same commit and there is
// nothing left for it to reconcile. The old tip has to be named explicitly,
// which is why this takes both ends.
//
// read-tree is the plumbing git switch itself uses for this. It refuses rather
// than discarding anything it would have to overwrite.
func (c Client) SwitchTree(ctx context.Context, from, to string) error {
	if err := safeRef(from); err != nil {
		return err
	}
	if err := safeRef(to); err != nil {
		return err
	}
	_, err := c.run(ctx, "read-tree", "-m", "-u", from, to)
	return err
}

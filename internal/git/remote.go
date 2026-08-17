// Everything that writes a ref or reaches the network.
//
// These are grouped by the safety property they hold rather than by the object
// they act on: fetching writes only under g2g's own namespace, pushing is
// atomic and lease-pinned, and pinning a fork point is what keeps it reachable.
// Interleaving them with the read-only queries hid which was which.
package git

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

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

// UnpinForkPoint drops the ref for a branch g2g no longer records.
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

// FetchIsolated downloads the named branches into g2g's own ref namespace,
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

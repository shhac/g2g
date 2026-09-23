package git

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/shhac/g2g/internal/subprocess"
)

// ErrNoSuchRemote is a remote this repository does not have. It is an
// ordinary answer for a repository nothing has been published from, and a
// caller may take it as one; any other failure is a failure.
var ErrNoSuchRemote = errors.New("no such remote")

// KnownTips answers what the remote held for each branch when this repository
// last heard from it, from local refs alone: nothing is asked of the network.
// A branch it has never heard of is absent from the answer.
//
// Two refs know, and neither always knows best. A push moves the user's
// remote-tracking ref; a pull fetches into g2g's own and leaves that one where
// it was, so after every pull the trunk's remote-tracking ref is behind. Where
// one descends from the other the descendant is the later knowledge. Where they
// are not in order — one side was rewritten — the remote-tracking ref wins,
// because a push is what moves it and it is what git status compares with.
//
// Whether a branch is there at all is the remote-tracking refs' to say, wherever
// this repository keeps any. g2g's own ref is only ever fetched and never
// pruned, so a branch deleted on the remote — which is what merging and
// deleting it does — lived on in it and read as published for good.
func (c Client) KnownTips(ctx context.Context, remote string, branches []string) (map[string]string, error) {
	if err := c.Remote(ctx, remote); err != nil {
		// git says a remote is not there with status 2, and nothing else there.
		if code, exited := subprocess.ExitCode(err); exited && code == 2 {
			return nil, fmt.Errorf("%w %q", ErrNoSuchRemote, remote)
		}
		return nil, err
	}
	for _, branch := range branches {
		if err := safeRef(branch); err != nil {
			return nil, err
		}
	}
	// One process for every ref under both prefixes. Resolving them by name
	// fails the whole batch on the first branch never pushed, which is most
	// of them in a fresh stack, and falls back to a process per ref.
	output, err := c.run(ctx, "for-each-ref", "--format=%(objectname) %(refname)", trackingRef(remote, ""), IsolatedRef(remote, ""))
	if err != nil {
		return nil, err
	}
	resolved := map[string]string{}
	tracking := false
	for _, line := range outputLines(output) {
		if object, ref, found := strings.Cut(line, " "); found {
			resolved[ref] = object
			tracking = tracking || strings.HasPrefix(ref, trackingRef(remote, ""))
		}
	}
	tips := make(map[string]string, len(branches))
	for _, branch := range branches {
		tracked, fetched := resolved[trackingRef(remote, branch)], resolved[IsolatedRef(remote, branch)]
		if tracked == "" && tracking {
			continue
		}
		tip, err := c.later(ctx, tracked, fetched)
		if err != nil {
			return nil, err
		}
		if tip != "" {
			tips[branch] = tip
		}
	}
	return tips, nil
}

// later is whichever of the two tips is the more recent knowledge, preferring
// tracking wherever that cannot be told from history.
func (c Client) later(ctx context.Context, tracking, fetched string) (string, error) {
	if fetched == "" || fetched == tracking {
		return tracking, nil
	}
	if tracking == "" {
		return fetched, nil
	}
	ahead, err := c.IsAncestor(ctx, tracking, fetched)
	if err != nil || !ahead {
		return tracking, err
	}
	return fetched, nil
}

func trackingRef(remote, branch string) string {
	return "refs/remotes/" + remote + "/" + branch
}

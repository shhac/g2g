package git

import (
	"context"
	"strings"
)

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
func (c Client) KnownTips(ctx context.Context, remote string, branches []string) (map[string]string, error) {
	if err := c.Remote(ctx, remote); err != nil {
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
	for _, line := range outputLines(output) {
		if object, ref, found := strings.Cut(line, " "); found {
			resolved[ref] = object
		}
	}
	tips := make(map[string]string, len(branches))
	for _, branch := range branches {
		tracking, fetched := resolved[trackingRef(remote, branch)], resolved[IsolatedRef(remote, branch)]
		tip, err := c.later(ctx, tracking, fetched)
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

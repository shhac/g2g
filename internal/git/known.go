package git

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/shhac/g2g/internal/parallel"
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
	// After every push and pull the two refs of each branch differ, so which
	// is later is a question per branch; they are asked at once, into a slice
	// sized first so each owns its element.
	answers := make([]string, len(branches))
	err = parallel.Each(ctx, branches, func(ctx context.Context, index int, branch string) error {
		tracked, fetched := resolved[trackingRef(remote, branch)], resolved[IsolatedRef(remote, branch)]
		if tracked == "" && tracking {
			return nil
		}
		tip, err := c.later(ctx, tracked, fetched)
		answers[index] = tip
		return err
	})
	if err != nil {
		return nil, err
	}
	tips := make(map[string]string, len(branches))
	for index, branch := range branches {
		if answers[index] != "" {
			tips[branch] = answers[index]
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

// IsolatedTips is every branch g2g has fetched from the remote, at the commit
// it fetched, in one process and without asking the remote anything.
func (c Client) IsolatedTips(ctx context.Context, remote string) (map[string]string, error) {
	if err := subprocess.CheckArgument("git", "remote name", remote); err != nil {
		return nil, err
	}
	prefix := IsolatedRef(remote, "")
	output, err := c.run(ctx, "for-each-ref", "--format=%(objectname) %(refname)", prefix)
	if err != nil {
		return nil, err
	}
	tips := map[string]string{}
	for _, line := range outputLines(output) {
		if object, ref, found := strings.Cut(line, " "); found {
			tips[strings.TrimPrefix(ref, prefix)] = object
		}
	}
	return tips, nil
}

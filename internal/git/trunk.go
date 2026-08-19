package git

import (
	"context"
	"strings"

	"github.com/shhac/g2g/internal/subprocess"
)

// defaultRemote is where a repository's default branch is looked up when a
// caller has no opinion. It is the same default push uses, stated once.
const defaultRemote = "origin"

// DefaultBranch reports the branch the remote considers its default, from
// refs/remotes/<remote>/HEAD.
//
// It answers the question "is this branch a trunk" for the ordinary case,
// locally and with no network: clone writes that ref, so it is already there.
//
// It is evidence and never authority. The ref can be missing — a repository
// built by hand has none — and it can be stale, so it is only ever used to
// choose how to phrase advice, never to decide structure. An unset ref is not
// a failure: it is a repository that has not been told, which is why this
// answers with an empty string rather than an error.
func (c Client) DefaultBranch(ctx context.Context, remote string) (string, error) {
	if remote == "" {
		remote = defaultRemote
	}
	if err := subprocess.CheckArgument("git", "remote name", remote); err != nil {
		return "", err
	}
	ref := "refs/remotes/" + remote + "/HEAD"
	output, err := c.run(ctx, "symbolic-ref", "--quiet", ref)
	if err != nil {
		// --quiet exits non-zero when the ref is not a symbolic ref, which is
		// the ordinary "nobody told this repository" case rather than a fault.
		return "", nil
	}
	resolved := strings.TrimSpace(string(output))
	prefix := "refs/remotes/" + remote + "/"
	if !strings.HasPrefix(resolved, prefix) {
		return "", nil
	}
	return strings.TrimPrefix(resolved, prefix), nil
}

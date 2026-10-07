package git

import (
	"context"
	"strings"

	"github.com/shhac/g2g/internal/subprocess"
)

// DefaultRemote is the remote every command reads from and publishes to when
// none is named, and so the one advice can leave unnamed. It is stated once
// because a suggestion that omits --remote is only right while the command it
// suggests defaults to the same remote.
const DefaultRemote = "origin"

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
		remote = DefaultRemote
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

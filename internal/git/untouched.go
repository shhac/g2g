package git

import (
	"context"
	"slices"
	"strings"
)

// untouchedPathLimit is how many paths a branch may touch before the question
// is not worth asking this way: a branch that rewrites that much of the tree
// is rare, and its pathspec would be a very long argument list.
const untouchedPathLimit = 400

// Untouched reports that nothing base gained since the branch left it touches
// a path the branch touches — which is enough to know the branch has not
// landed there, without comparing any content.
//
// Whether a branch has landed is otherwise a patch-id comparison against every
// commit the base gained since the branch left it, and on a long-lived trunk
// that is most of a status: over a second for a branch forked a few thousand
// commits back, for each branch, every time. Asking which paths those commits
// touch is a walk git answers in milliseconds.
//
// It answers only yes or "could not tell", never "has landed". Yes needs both:
//   - every path any of the branch's own commits touches is one base has not
//     touched, so none of those commits can have an equivalent there — an
//     equivalent patch touches the same paths;
//   - every path the branch changes as a whole is one base has not touched, so
//     merging it in would change base, which is what rules out a squash.
//
// A branch whose commits touch nothing, or cancel out, or that touches more
// than untouchedPathLimit paths, is "could not tell", and the full comparison
// answers it.
func (c Client) Untouched(ctx context.Context, base, branch, limit string) (bool, error) {
	for _, ref := range []string{base, branch, limit} {
		if ref == "" {
			continue
		}
		if err := safeRef(ref); err != nil {
			return false, err
		}
	}
	own := []string{"log", "--no-renames", "--format=", "--name-only", "-z", branch, "^" + base}
	if limit != "" {
		own = append(own, "^"+limit)
	}
	touched, err := c.run(ctx, own...)
	if err != nil {
		return false, err
	}
	changed, err := c.run(ctx, "diff", "--no-renames", "--name-only", "-z", base+"..."+branch)
	if err != nil {
		return false, err
	}
	perCommit, whole := nulSeparated(touched), nulSeparated(changed)
	if len(perCommit) == 0 || len(whole) == 0 {
		return false, nil
	}
	paths := slices.Compact(slices.Sorted(slices.Values(append(perCommit, whole...))))
	if len(paths) > untouchedPathLimit {
		return false, nil
	}
	args := append([]string{"--literal-pathspecs", "rev-list", "-1", base, "^" + branch, "--"}, paths...)
	output, err := c.run(ctx, args...)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(output)) == "", nil
}

// nulSeparated splits -z output exactly: a name is whatever lies between the
// separators, spaces and all, because a name altered here is a path the
// pathspec would not match — which would read as untouched.
func nulSeparated(output []byte) []string {
	fields := strings.Split(string(output), "\x00")
	paths := make([]string, 0, len(fields))
	for _, field := range fields {
		if field != "" {
			paths = append(paths, field)
		}
	}
	return paths
}

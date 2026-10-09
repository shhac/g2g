package git

import (
	"context"
	"strings"
)

// CherryDropped returns the commits in upstream..head whose content is not
// already present in upstream.
//
// This separates a commit a rewritten parent genuinely dropped from one it
// merely rewrote. The distinction decides whether a child may absorb what its
// parent no longer has: absorbing a rewritten commit would give the child a
// stale duplicate of work the parent still carries under a new object id.
func (c Client) CherryDropped(ctx context.Context, upstream, head string) ([]string, error) {
	kept, _, err := c.Cherry(ctx, upstream, head, "")
	return kept, err
}

// Cherry compares the commits in limit..head against upstream by content,
// returning those with no equivalent there and those with one.
//
// limit is optional and narrows the comparison to a branch's own commits,
// which is what distinguishes "this branch has nothing left to contribute"
// from "some commit somewhere below it is already upstream".
func (c Client) Cherry(ctx context.Context, upstream, head, limit string) (absent, present []string, err error) {
	if err := safeRef(upstream); err != nil {
		return nil, nil, err
	}
	if err := safeRef(head); err != nil {
		return nil, nil, err
	}
	args := []string{"cherry", upstream, head}
	if limit != "" {
		if err := safeRef(limit); err != nil {
			return nil, nil, err
		}
		args = append(args, limit)
	}
	output, err := c.run(ctx, args...)
	if err != nil {
		return nil, nil, err
	}
	absent, present = make([]string, 0), make([]string, 0)
	for _, line := range outputLines(output) {
		mark, object, found := strings.Cut(line, " ")
		if !found {
			continue
		}
		// A leading + means no equivalent content upstream; a leading - means
		// the same content is already there under another object id.
		if mark == "+" {
			absent = append(absent, object)
			continue
		}
		present = append(present, object)
	}
	return absent, present, nil
}

// absorbedMinorVersion is the first Git minor release with
// git merge-tree --write-tree.
const absorbedMinorVersion = 38

// Absorbed reports whether merging a branch into a base would change the base
// at all: its work is already there, however it arrived.
//
// This is the question git cherry cannot answer. A squash merge combines a
// branch's commits into one, so the result is content-equivalent to none of
// them individually and cherry marks every one as new — on the commonest way a
// branch lands. Merging the branch in and finding the base's own tree back is
// the whole-branch equivalent of the per-commit test.
//
// A merge that conflicts exits non-zero, which is an answer rather than a
// failure: content that conflicts is content the base does not already have --
// as of now. A squash the trunk has since edited over conflicts too, so a
// conflict asks the same question of the trunk as it was when those lines
// last changed; see absorbedEarlier.
func (c Client) Absorbed(ctx context.Context, base, branch string) (bool, error) {
	if err := safeRef(base); err != nil {
		return false, err
	}
	if err := safeRef(branch); err != nil {
		return false, err
	}
	supported, err := c.supportsMergeTree(ctx)
	if err != nil || !supported {
		return false, err
	}
	merged, err := c.run(ctx, "merge-tree", "--write-tree", base, branch)
	if err != nil {
		return c.absorbedEarlier(ctx, base, branch), nil
	}
	return c.sameTree(ctx, merged, base)
}

// absorbedCandidates bounds how far back absorbedEarlier looks. Past it the
// answer is no, which costs only the case where the squash is buried under
// that many later edits to the same files.
const absorbedCandidates = 32

// absorbedPaths bounds how many files a branch may touch for the earlier look
// to be tried at all, since each is an argument to one command.
const absorbedPaths = 256

// absorbedEarlier asks whether the branch was absorbed by the trunk at some
// commit since they parted, rather than by its tip.
//
// A squash merge and then a later edit to the same lines is ordinary -- a
// follow-up fix, a refactor -- and merging the branch into the trunk as it is
// now conflicts, because the two sides changed those lines differently. The
// branch still landed; the trunk moved on from it. Asked of the trunk as it
// was at the squash, the merge gives that commit's own tree back.
//
// Only commits touching the branch's own files can have absorbed it, so only
// those are asked. It is the same test as the tip's, so it adds no new way to
// be wrong: whatever it calls absorbed, merging the branch into an ancestor of
// the base changed nothing. It is reached only on a conflict, which a plain
// revert of the squash does not produce, so a branch whose work was taken back
// out is not read as landed. Anything it cannot answer is no.
func (c Client) absorbedEarlier(ctx context.Context, base, branch string) bool {
	forked, err := c.run(ctx, "merge-base", base, branch)
	if err != nil {
		return false
	}
	since := strings.TrimSpace(string(forked))
	changed, err := c.run(ctx, "diff", "--name-only", "--no-renames", "-z", since, branch)
	if err != nil {
		return false
	}
	paths := strings.FieldsFunc(string(changed), func(r rune) bool { return r == 0 })
	if len(paths) == 0 || len(paths) > absorbedPaths {
		return false
	}
	// Literal, because a file name is not a pattern: one called "*" would
	// otherwise select every commit in the range.
	args := append([]string{"--literal-pathspecs", "rev-list", "--reverse", since + ".." + base, "--"}, paths...)
	touched, err := c.run(ctx, args...)
	if err != nil {
		return false
	}
	candidates := outputLines(touched)
	if len(candidates) > absorbedCandidates {
		return false
	}
	for _, candidate := range candidates {
		merged, err := c.run(ctx, "merge-tree", "--write-tree", candidate, branch)
		if err != nil {
			continue
		}
		if same, err := c.sameTree(ctx, merged, candidate); err == nil && same {
			return true
		}
	}
	return false
}

// sameTree reports whether merge-tree's answer is the revision's own tree.
func (c Client) sameTree(ctx context.Context, merged []byte, revision string) (bool, error) {
	tree, err := c.run(ctx, "rev-parse", revision+"^{tree}")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(merged)) == strings.TrimSpace(string(tree)), nil
}

// supportsMergeTree gates the question the same way replay is gated: verified
// versions only, and answering false costs the squash-merge case and nothing
// else.
func (c Client) supportsMergeTree(ctx context.Context) (bool, error) {
	output, err := c.run(ctx, "--version")
	if err != nil {
		return false, err
	}
	major, minor, err := parseGitVersion(output)
	if err != nil {
		return false, err
	}
	return major > 2 || (major == 2 && minor >= absorbedMinorVersion), nil
}

// Commits lists the commits reachable from tip and from none of excluded, by
// id. Membership by id is the question when what matters is whether this
// exact commit was there: a rewritten copy of it is a different commit, and
// asking by content would let a re-added change read as one already seen.
func (c Client) Commits(ctx context.Context, tip string, excluded []string) ([]string, error) {
	if err := safeRef(tip); err != nil {
		return nil, err
	}
	args := []string{"rev-list", tip}
	if len(excluded) != 0 {
		args = append(args, "--not")
		for _, ref := range excluded {
			if err := safeRef(ref); err != nil {
				return nil, err
			}
			args = append(args, ref)
		}
	}
	output, err := c.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	return outputLines(output), nil
}

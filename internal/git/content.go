package git

import (
	"context"
	"fmt"
	"slices"
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
	if err := safeRefs(upstream, head); err != nil {
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
	if err := safeRefs(base, branch); err != nil {
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

// absorbedCandidates bounds how far back absorbedEarlier looks: the newest
// that many trunk commits touching the branch's files. Past it the answer is
// no, which costs only the case where the squash is buried under that many
// later edits to the same files.
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
	changed, err := c.run(ctx, "diff", "--raw", "--no-abbrev", "--no-renames", "-z", since, branch)
	if err != nil {
		return false
	}
	changes := rawChanges(changed)
	if len(changes) == 0 || len(changes) > absorbedPaths {
		return false
	}
	paths := make([]string, 0, len(changes))
	for _, change := range changes {
		paths = append(paths, change.path)
	}
	held, before := c.heldAdded(ctx, since, base, addedFiles(changes))
	if !held {
		return false
	}
	// Literal, because a file name is not a pattern: one called "*" would
	// otherwise select every commit in the range. Newest first, and only that
	// many: what bounds the look is how deep the squash is buried, not how
	// busy those files were before it. Counting every edit since the fork gave
	// up on a branch whose files the trunk had touched often while it was in
	// review, and land then replayed the branch it had just merged.
	//
	// Each candidate comes with its tree, from the same process, which is what
	// a clean merge is compared with: asking for it after each merge spawned a
	// second process per candidate, and on a branch that conflicts with all
	// of them that doubled the cost of the answer no.
	range_ := []string{since + ".." + base}
	if before != "" {
		range_ = []string{before + "^@", "^" + since}
	}
	args := append([]string{"--literal-pathspecs", "log", "--format=%H %T", fmt.Sprintf("--max-count=%d", absorbedCandidates)}, range_...)
	args = append(append(args, "--"), paths...)
	touched, err := c.run(ctx, args...)
	if err != nil {
		return false
	}
	for _, line := range outputLines(touched) {
		candidate, tree, found := strings.Cut(line, " ")
		if !found {
			continue
		}
		merged, err := c.run(ctx, "merge-tree", "--write-tree", candidate, branch)
		if err == nil && strings.TrimSpace(string(merged)) == tree {
			return true
		}
	}
	return false
}

// absorbedWitnesses bounds how many of a branch's new files are looked for in
// the base, since each is two arguments to one command.
const absorbedWitnesses = 16

// rawChange is one file from git's --raw output: the object it was, the one
// it became and what happened to it -- and, read from a log, in which commit.
type rawChange struct {
	status, was, object, path, commit string
}

// rawCommit is the format that names each commit in a log read by rawChanges.
const rawCommit = "--format=commit %H"

// rawChanges reads `--raw --no-abbrev --no-renames -z` output: a record of
// modes, objects and a status, then the path it is about, each ended by NUL.
func rawChanges(output []byte) []rawChange {
	fields := strings.Split(string(output), "\x00")
	changes := make([]rawChange, 0, len(fields)/2)
	commit := ""
	for i := 0; i+1 < len(fields); i++ {
		field := strings.TrimLeft(fields[i], "\n")
		if named, found := strings.CutPrefix(field, "commit "); found {
			commit = named
			continue
		}
		record, found := strings.CutPrefix(field, ":")
		if !found {
			continue
		}
		parts := strings.Fields(record)
		if len(parts) != 5 {
			continue
		}
		i++
		changes = append(changes, rawChange{status: parts[4], was: parts[2], object: parts[3], path: fields[i], commit: commit})
	}
	return changes
}

// addedFiles is the files a branch adds, when those can witness for it: none
// if the branch deletes anything, because a deleted file and an added one may
// be one file renamed, and a merge follows a rename where a path does not.
func addedFiles(changes []rawChange) []rawChange {
	added := make([]rawChange, 0, absorbedWitnesses)
	for _, change := range changes {
		if change.status == "D" {
			return nil
		}
		if change.status == "A" && len(added) < absorbedWitnesses {
			added = append(added, change)
		}
	}
	return added
}

// heldAdded reports whether the base, at some commit since the fork, held
// every one of the files a branch adds exactly as the branch has it -- and,
// when the base has changed one of them since, the commit that first did,
// which nothing from there on can have absorbed the branch. It only ever
// rules commits out.
//
// A trunk that absorbed a branch holds the files the branch added, as the
// branch has them: a file both sides added differently is a conflict, and one
// the trunk lacks is a change the merge would make. So a branch one of whose
// new files never appeared in the trunk was absorbed by none of its commits,
// and one whose new file the trunk went on to edit was absorbed, if at all,
// before that edit. Both are one walk of the trunk's history rather than a
// merge at each of the candidates. A branch that conflicts with a busy trunk
// paid for every one of them, each time anything asked whether it had landed,
// and so did a branch that had landed and been edited over since: on a
// repository of a hundred stacks, most of them finished and not yet pruned,
// that was two thousand merges and most of a status.
//
// It is right about every history that takes a branch's files in where the
// branch put them and edits them on the line that took them. Where it is not
// -- a trunk that moved them as it took them, or holds them on a line the
// edit never reached -- the answer is that the branch has not landed, which
// is the direction that loses nothing. A branch that adds no file, or deletes
// one, is not asked, and a failure to ask rules nothing out.
func (c Client) heldAdded(ctx context.Context, since, base string, added []rawChange) (held bool, before string) {
	if len(added) == 0 {
		return true, ""
	}
	// -m, because a merge is otherwise shown with no changes at all, and a
	// file that arrived through one would read as never having arrived.
	args := []string{"--literal-pathspecs", "log", rawCommit, "--raw", "--no-abbrev", "--no-renames", "-z", "-m"}
	type file struct{ object, path string }
	const unseen, arrived, edited = 0, 1, 2
	latest := make(map[file]int, len(added))
	paths := make([]string, 0, len(added))
	for _, witness := range added {
		args = append(args, "--find-object="+witness.object)
		latest[file{witness.object, witness.path}] = unseen
		paths = append(paths, witness.path)
	}
	output, err := c.run(ctx, append(append(args, since+".."+base, "--"), paths...)...)
	if err != nil {
		return true, ""
	}
	// Newest first, so the first thing read about a file is the last thing
	// that happened to it, and the last edit read is the oldest of those.
	for _, change := range rawChanges(output) {
		took, lost := file{change.object, change.path}, file{change.was, change.path}
		if state, wanted := latest[took]; wanted && state == unseen {
			latest[took] = arrived
		}
		if state, wanted := latest[lost]; wanted && state == unseen && change.was != change.object {
			latest[lost] = edited
			before = change.commit
		}
	}
	for _, state := range latest {
		if state == unseen {
			return false, ""
		}
	}
	return true, before
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

// Unreferenced reports a revision with commits that no branch, tag or
// remote's copy reaches -- g2g's own fetched copies included -- which is what
// switching away from a detached HEAD there would leave to the reflog alone.
func (c Client) Unreferenced(ctx context.Context, revision string) (bool, error) {
	if err := safeRef(revision); err != nil {
		return false, err
	}
	output, err := c.run(ctx, "rev-list", "-n", "1", revision, "--not", "--branches", "--tags", "--remotes", "--glob="+strings.TrimSuffix(isolatedRemotePrefix, "/"))
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(output)) != "", nil
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

// Inequivalent lists the commits on each side of left...right with no
// equivalent by content on the other side, both sides bounded by bounds.
// Merges are left out, as git cherry leaves them out.
//
// It is Cherry with both sides bounded. Cherry compares head's commits with
// everything upstream has that head does not, and after a restack that is
// every commit the trunk gained since: a cost in the trunk's history rather
// than the branch's. Bounded, fewer commits are there to match, so a commit
// matched here is matched there too and an empty answer is exact; a commit
// listed here may still have an equivalent somewhere below the bounds.
func (c Client) Inequivalent(ctx context.Context, left, right string, bounds []string) (leftOnly, rightOnly []string, err error) {
	if err := safeRefs(left, right); err != nil {
		return nil, nil, err
	}
	args := []string{"rev-list", "--left-right", "--cherry-mark", "--no-merges", left + "..." + right}
	if len(bounds) != 0 {
		args = append(args, "--not")
		for _, ref := range bounds {
			if err := safeRef(ref); err != nil {
				return nil, nil, err
			}
			args = append(args, ref)
		}
	}
	output, err := c.run(ctx, args...)
	if err != nil {
		return nil, nil, err
	}
	leftOnly, rightOnly = make([]string, 0), make([]string, 0)
	for _, line := range outputLines(output) {
		switch {
		case strings.HasPrefix(line, "<"):
			leftOnly = append(leftOnly, line[1:])
		case strings.HasPrefix(line, ">"):
			rightOnly = append(rightOnly, line[1:])
		}
	}
	return leftOnly, rightOnly, nil
}

// Refs is each of the named refs that exists, with the commit it points at --
// only those that contain commit, when one is given. A name is matched
// exactly, though for-each-ref would also list what sits below it. One
// process answers which of many branches hold a commit, where asking each
// was one per branch.
func (c Client) Refs(ctx context.Context, refs []string, contains string) (map[string]string, error) {
	found := make(map[string]string, len(refs))
	if len(refs) == 0 {
		return found, nil
	}
	args := []string{"for-each-ref", "--format=%(refname)%09%(objectname)"}
	if contains != "" {
		if err := safeRef(contains); err != nil {
			return nil, err
		}
		args = append(args, "--contains="+contains)
	}
	for _, ref := range refs {
		if err := safeRef(ref); err != nil {
			return nil, err
		}
	}
	output, err := c.run(ctx, append(args, refs...)...)
	if err != nil {
		return nil, err
	}
	for _, line := range outputLines(output) {
		ref, object, ok := strings.Cut(line, "\t")
		if ok && slices.Contains(refs, ref) {
			found[ref] = object
		}
	}
	return found, nil
}

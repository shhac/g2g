package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Range is the commits to replay: everything reachable from To but not From.
// From is the fork point, so the range is exactly the branch's own work.
type Range struct {
	From string
	To   string
}

func (r Range) spec() string { return r.From + ".." + r.To }

// RefUpdate is one branch a rewrite moves.
type RefUpdate struct {
	Ref string
	New string
	Old string
}

// Branch strips the refs/heads/ prefix a ref update carries.
func (u RefUpdate) Branch() string { return strings.TrimPrefix(u.Ref, "refs/heads/") }

// replayMinorVersion is the first Git minor release with git replay.
const replayMinorVersion = 44

// SupportsReplay reports whether this Git can replay commits without a
// checkout, which is the difference between a rewrite that leaves the working
// tree alone and one that takes it over.
//
// It is gated the same way the Graphite CLI is: verified versions only, and no
// attempt to guess. Answering false costs the conflict prediction and the
// untouched checkout, and costs nothing else.
func (c Client) SupportsReplay(ctx context.Context) (bool, error) {
	output, err := c.run(ctx, "--version")
	if err != nil {
		return false, err
	}
	major, minor, err := parseGitVersion(output)
	if err != nil {
		return false, err
	}
	return major > 2 || (major == 2 && minor >= replayMinorVersion), nil
}

// parseGitVersion reads the major and minor from `git --version`.
//
// It is separate from the call that produces that output so the version matrix
// is a table of strings rather than five spawned processes, which is what the
// Graphite adapter's checkVersion already does one package over.
func parseGitVersion(output []byte) (major, minor int, err error) {
	fields := strings.Fields(strings.TrimSpace(string(output)))
	if len(fields) < 3 {
		return 0, 0, fmt.Errorf("parse git --version output")
	}
	parts := strings.Split(fields[2], ".")
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("parse git version %q", fields[2])
	}
	major, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("parse git version %q", fields[2])
	}
	minor, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("parse git version %q", fields[2])
	}
	return major, minor, nil
}

// PreviewReplay reports exactly which refs a rewrite would move, without
// moving any of them.
//
// This is what lets a preview show real object ids and predict a conflict
// before anything is touched. A conflict is reported as a false result rather
// than an error, because "this will not apply cleanly" is an answer.
func (c Client) PreviewReplay(ctx context.Context, onto string, ranges []Range) (updates []RefUpdate, clean bool, err error) {
	output, err := c.replay(ctx, onto, ranges, "--ref-action=print")
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return parseRefUpdates(output), true, nil
}

// Replay rewrites every range onto a new base in one atomic transaction,
// without checking anything out. A conflict moves no ref at all and leaves no
// in-progress state behind, which is why it is tried before any rebase.
func (c Client) Replay(ctx context.Context, onto string, ranges []Range) error {
	_, err := c.replay(ctx, onto, ranges)
	return err
}

func (c Client) replay(ctx context.Context, onto string, ranges []Range, extra ...string) ([]byte, error) {
	if err := safeRef(onto); err != nil {
		return nil, err
	}
	if len(ranges) == 0 {
		return nil, fmt.Errorf("no commit ranges selected for replay")
	}
	args := append([]string{"replay", "--onto=" + onto}, extra...)
	for _, replayed := range ranges {
		if err := safeRef(replayed.From); err != nil {
			return nil, err
		}
		if err := safeRef(replayed.To); err != nil {
			return nil, err
		}
		args = append(args, replayed.spec())
	}
	return c.run(ctx, args...)
}

func parseRefUpdates(output []byte) []RefUpdate {
	updates := make([]RefUpdate, 0)
	for _, line := range outputLines(output) {
		fields := strings.Fields(line)
		if len(fields) != 4 || fields[0] != "update" {
			continue
		}
		updates = append(updates, RefUpdate{Ref: fields[1], New: fields[2], Old: fields[3]})
	}
	return updates
}

// Rebase replays a branch's own commits onto a new base in the working tree,
// updating every branch along the way.
//
// This is the resumable engine, used only once a preview has established that
// the rewrite conflicts. It necessarily takes over the checkout: resolving a
// conflict needs a working tree the user can edit, and that is theirs.
func (c Client) Rebase(ctx context.Context, onto string, replayed Range) error {
	if err := safeRef(onto); err != nil {
		return err
	}
	if err := safeRef(replayed.From); err != nil {
		return err
	}
	if err := safeRef(replayed.To); err != nil {
		return err
	}
	// Both flags exist to stop this depending on which Git is installed, and
	// neither is a preference.
	//
	// --no-reapply-cherry-picks drops a commit whose content is already in the
	// new base, which is precisely what repairs a stack after a squash merge;
	// older Git reapplies it instead.
	//
	// --empty=drop decides what happens when a commit becomes empty as a
	// result. Older Git stops and waits, which reads as a conflict but leaves
	// no conflicted file and nothing for a person to resolve — the commit
	// simply has nothing left to say. Newer Git drops it and carries on.
	//
	// --update-refs is deliberately absent: each branch is rebased on its own,
	// so there are no intermediate refs to carry, and it is the part that
	// behaved differently across versions.
	_, err := c.run(ctx, "rebase", "--onto", onto, replayed.From, replayed.To,
		"--no-reapply-cherry-picks", "--empty=drop")
	return err
}

// RebaseContinue resumes an interrupted rebase once conflicts are resolved.
func (c Client) RebaseContinue(ctx context.Context) error {
	return c.rebaseStep(ctx, "--continue")
}

// RebaseSkip abandons the commit an interrupted rebase stopped on.
func (c Client) RebaseSkip(ctx context.Context) error { return c.rebaseStep(ctx, "--skip") }

// RebaseAbort restores every branch the interrupted rebase touched.
func (c Client) RebaseAbort(ctx context.Context) error { return c.rebaseStep(ctx, "--abort") }

func (c Client) rebaseStep(ctx context.Context, step string) error {
	// git opens an editor to confirm a continued commit message unless told
	// otherwise, which would hang a command that has no terminal. Overriding
	// the editor through -c keeps this inside argv rather than needing the
	// process seam to carry environment.
	_, err := c.run(ctx, "-c", "core.editor=true", "rebase", step)
	return err
}

// RebaseInProgress reports whether this worktree is part-way through a rebase,
// whether or not g2g is the one that started it.
func (c Client) RebaseInProgress(ctx context.Context) (bool, error) {
	for _, name := range []string{"rebase-merge", "rebase-apply"} {
		output, err := c.run(ctx, "rev-parse", "--git-path", name)
		if err != nil {
			return false, err
		}
		if _, err := os.Stat(strings.TrimSpace(string(output))); err == nil {
			return true, nil
		}
	}
	return false, nil
}

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

// ConflictedPaths lists the files an interrupted rewrite left unmerged.
//
// Telling someone a rewrite stopped without telling them where is most of the
// way to telling them nothing: these are the files they have to open.
func (c Client) ConflictedPaths(ctx context.Context) ([]string, error) {
	output, err := c.run(ctx, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0)
	for _, line := range outputLines(output) {
		paths = append(paths, line)
	}
	return paths, nil
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
		return fmt.Errorf("%s has diverged from %s and cannot be fast-forwarded; reconcile it yourself", branch, to)
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

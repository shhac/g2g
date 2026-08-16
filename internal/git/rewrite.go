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
// checkout. The command is documented as experimental, so it is gated the same
// way the Graphite CLI is: recognised versions only, and no attempt to guess.
func (c Client) SupportsReplay(ctx context.Context) (bool, error) {
	output, err := c.run(ctx, "--version")
	if err != nil {
		return false, err
	}
	fields := strings.Fields(strings.TrimSpace(string(output)))
	if len(fields) < 3 {
		return false, fmt.Errorf("parse git --version output")
	}
	parts := strings.Split(fields[2], ".")
	if len(parts) < 2 {
		return false, fmt.Errorf("parse git version %q", fields[2])
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return false, fmt.Errorf("parse git version %q", fields[2])
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return false, fmt.Errorf("parse git version %q", fields[2])
	}
	return major > 2 || (major == 2 && minor >= replayMinorVersion), nil
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
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
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
	_, err := c.run(ctx, "rebase", "--onto", onto, replayed.From, replayed.To, "--update-refs")
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
// whether or not gt2gh is the one that started it.
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

// ResetKeep points the index and working tree at the current branch tip.
//
// A replay moves refs without touching the working tree, so a user standing on
// a rewritten branch is left with an index describing the old commit — which
// git reports as changes they never made. This resyncs it, and refuses rather
// than discarding anything it would have to overwrite.
func (c Client) ResetKeep(ctx context.Context) error {
	_, err := c.run(ctx, "reset", "--keep", "HEAD")
	return err
}

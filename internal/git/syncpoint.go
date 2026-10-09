package git

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/subprocess"
)

// Sync points: where this clone and a remote last agreed on a branch.
//
// They are g2g's own refs, as refs/g2g/remotes/ is, and never the user's. Each
// is written with a reflog, so every earlier agreement — and with it every
// commit a later pull or push dropped — stays reachable through
// git reflog until git expires the entry.

const syncedPrefix = "refs/g2g/synced/"

// SyncedRef names the ref holding a branch's sync point for a remote.
func SyncedRef(remote, branch string) string {
	return syncedPrefix + remote + "/" + branch
}

// SyncPoint is one agreement: the remote's tip for the branch, and the
// branch's own tip, at the moment this clone and the remote last agreed on it.
type SyncPoint struct {
	Tip   string
	Local string
	// Dropped are commits the command that recorded this took off the branch,
	// recorded so they can be named afterwards.
	Dropped []string
	// Command is what recorded it, and At when.
	Command string
	At      time.Time
}

// RecordSync writes a branch's sync point. The message carries the local tip
// and anything dropped, which is how a later read checks the point describes
// this branch and how status names what was dropped.
func (c Client) RecordSync(ctx context.Context, remote, branch string, point SyncPoint) error {
	for _, value := range []string{remote, branch, point.Tip, point.Local} {
		if err := safeRef(value); err != nil {
			return err
		}
	}
	message := "g2g " + point.Command + " local=" + point.Local
	if len(point.Dropped) != 0 {
		message += " dropped=" + strings.Join(point.Dropped, ",")
	}
	_, err := c.run(ctx, "update-ref", "--create-reflog", "-m", message, SyncedRef(remote, branch), point.Tip)
	return err
}

// ReadSync reads a branch's latest sync point, and reports whether it has one.
func (c Client) ReadSync(ctx context.Context, remote, branch string) (SyncPoint, bool, error) {
	history, err := c.SyncHistory(ctx, remote, branch, 1)
	if err != nil || len(history) == 0 {
		return SyncPoint{}, false, err
	}
	return history[0], true, nil
}

// SyncHistory reads a branch's sync points, newest first, at most limit of
// them. A branch with none has an empty history.
func (c Client) SyncHistory(ctx context.Context, remote, branch string, limit int) ([]SyncPoint, error) {
	if err := safeRef(remote); err != nil {
		return nil, err
	}
	if err := safeRef(branch); err != nil {
		return nil, err
	}
	ref := SyncedRef(remote, branch)
	exists, err := c.refExists(ctx, ref)
	if err != nil || !exists {
		return nil, err
	}
	output, err := c.run(ctx, "reflog", "show", "--date=unix", "-n", strconv.Itoa(limit), "--format=%H%x09%gd%x09%gs", ref)
	if err != nil {
		return nil, err
	}
	points := make([]SyncPoint, 0, limit)
	for _, line := range outputLines(output) {
		if point, ok := parseSyncLine(line); ok {
			points = append(points, point)
		}
	}
	return points, nil
}

// SyncHistories is SyncHistory for many branches at once: each branch's sync
// points, newest first, keyed by branch, leaving out those with none. Two
// processes whatever the number of branches -- which sync refs exist, and
// every one of their reflogs in one walk -- where asking per branch was two
// each. A ref named that does not exist fails the whole walk, which is why the
// first question is asked.
//
// One unreadable reflog fails the walk for all of them, so a failed walk is
// asked again one ref at a time: what could be read comes back with the
// error, and a caller that can do without one branch's history does.
func (c Client) SyncHistories(ctx context.Context, remote string, branches []string) (map[string][]SyncPoint, error) {
	if err := safeRef(remote); err != nil {
		return nil, err
	}
	prefix := syncedPrefix + remote + "/"
	output, err := c.run(ctx, "for-each-ref", "--format=%(refname)", prefix)
	if err != nil {
		return nil, err
	}
	existing := outputLines(output)
	refs := make([]string, 0, len(branches))
	for _, branch := range branches {
		if ref := prefix + branch; slices.Contains(existing, ref) && !slices.Contains(refs, ref) {
			refs = append(refs, ref)
		}
	}
	histories := make(map[string][]SyncPoint, len(refs))
	if len(refs) == 0 {
		return histories, nil
	}
	args := append([]string{"log", "-g", "--date=unix", "--format=%H%x09%gD%x09%gs"}, refs...)
	output, err = c.run(ctx, append(args, "--")...)
	if err != nil {
		return c.syncHistoriesEach(ctx, remote, refs, prefix)
	}
	for _, line := range outputLines(output) {
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) != 3 {
			continue
		}
		ref, _, found := strings.Cut(fields[1], "@{")
		if !found || !strings.HasPrefix(ref, prefix) {
			continue
		}
		if point, ok := parseSyncLine(line); ok {
			branch := strings.TrimPrefix(ref, prefix)
			histories[branch] = append(histories[branch], point)
		}
	}
	return histories, nil
}

// syncHistoriesEach is SyncHistories one ref at a time, for when the walk over
// all of them failed.
func (c Client) syncHistoriesEach(ctx context.Context, remote string, refs []string, prefix string) (map[string][]SyncPoint, error) {
	histories := make(map[string][]SyncPoint, len(refs))
	failed := make([]error, 0)
	for _, ref := range refs {
		branch := strings.TrimPrefix(ref, prefix)
		history, err := c.SyncHistory(ctx, remote, branch, maxSyncHistory)
		if err != nil {
			failed = append(failed, err)
			continue
		}
		if len(history) != 0 {
			histories[branch] = history
		}
	}
	return histories, errors.Join(failed...)
}

// parseSyncLine reads one reflog line: the tip, the selector carrying the
// time, and the message g2g wrote. A line a person or another tool wrote
// carries no local tip and is not a sync point.
func parseSyncLine(line string) (SyncPoint, bool) {
	fields := strings.SplitN(line, "\t", 3)
	if len(fields) != 3 {
		return SyncPoint{}, false
	}
	point := SyncPoint{Tip: fields[0]}
	if open := strings.LastIndex(fields[1], "@{"); open >= 0 {
		if seconds, err := strconv.ParseInt(strings.TrimSuffix(fields[1][open+2:], "}"), 10, 64); err == nil {
			point.At = time.Unix(seconds, 0)
		}
	}
	words := strings.Fields(fields[2])
	if len(words) < 2 || words[0] != "g2g" {
		return SyncPoint{}, false
	}
	point.Command = words[1]
	for _, word := range words[2:] {
		key, value, _ := strings.Cut(word, "=")
		switch key {
		case "local":
			point.Local = value
		case "dropped":
			point.Dropped = strings.Split(value, ",")
		}
	}
	return point, point.Local != ""
}

// Describes reports whether a sync point was recorded for this branch rather
// than for another that had its name: the local tip it recorded is in the
// branch's history, or in its reflog. A branch deleted and created again under
// the same name has neither, and its old sync point says nothing about it.
func (c Client) Describes(ctx context.Context, branch string, point SyncPoint) (bool, error) {
	if point.Local == "" {
		return false, nil
	}
	contains, err := c.IsAncestor(ctx, point.Local, branch)
	if err != nil || contains {
		return contains, err
	}
	ref := "refs/heads/" + branch
	exists, err := c.reflogExists(ctx, ref)
	if err != nil || !exists {
		return false, err
	}
	output, err := c.run(ctx, "reflog", "show", "--format=%H", ref)
	if err != nil {
		return false, err
	}
	return slices.Contains(outputLines(output), point.Local), nil
}

// DescribesAll is Describes for many branches at once: every branch's reflog
// in one walk, and the ancestry question only for a branch whose reflog does
// not hold the tip its sync point recorded. That tip was the branch's own when
// it was recorded, so its reflog nearly always does, and asking per branch was
// three processes for each one rewritten since.
func (c Client) DescribesAll(ctx context.Context, points map[string]SyncPoint) (map[string]bool, error) {
	described := make(map[string]bool, len(points))
	branches := slices.Sorted(maps.Keys(points))
	refs := make([]string, 0, len(branches))
	for _, branch := range branches {
		if err := safeRef(branch); err != nil {
			return nil, err
		}
		if points[branch].Local != "" {
			refs = append(refs, "refs/heads/"+branch)
		}
	}
	if len(refs) == 0 {
		return described, nil
	}
	args := append([]string{"log", "-g", "--ignore-missing", "--format=%gD%x09%H"}, refs...)
	output, err := c.run(ctx, append(args, "--")...)
	if err != nil {
		return nil, err
	}
	for _, line := range outputLines(output) {
		selector, commit, found := strings.Cut(line, "\t")
		ref, _, selected := strings.Cut(selector, "@{")
		branch := strings.TrimPrefix(ref, "refs/heads/")
		if found && selected && points[branch].Local == commit {
			described[branch] = true
		}
	}
	for _, branch := range branches {
		point := points[branch]
		if described[branch] || point.Local == "" {
			continue
		}
		contains, err := c.IsAncestor(ctx, point.Local, branch)
		if err != nil {
			return nil, err
		}
		described[branch] = contains
	}
	return described, nil
}

// ForgetSync removes a branch's sync points for every remote, with their
// reflogs. It is for a branch that is gone: one that comes back under the same
// name is different work.
func (c Client) ForgetSync(ctx context.Context, branch string) error {
	if err := safeRef(branch); err != nil {
		return err
	}
	remotes, err := c.remotes(ctx)
	if err != nil {
		return err
	}
	for _, remote := range remotes {
		if err := c.deleteRef(ctx, SyncedRef(remote, branch)); err != nil {
			return err
		}
	}
	return nil
}

// forgetSync is ForgetSync for a branch this client has just deleted. The
// branch is gone either way, and a sync point left behind is ignored once a
// branch of that name comes back, so failing to remove it is a warning.
func (c Client) forgetSync(ctx context.Context, branch string) {
	if err := c.ForgetSync(ctx, branch); err != nil {
		diagnostic.Warn(ctx, "delete.sync_point", fmt.Sprintf("%s is deleted; its sync point could not be removed", branch))
	}
}

// MoveSync carries a branch's sync points to its new name, for every remote:
// the whole history, oldest first, since the history is what keeps every
// commit a pull or push dropped reachable. The commits are the same ones, so
// every agreement still holds. Each entry is dated by the move rather than
// when it was first recorded, which a reflog written with update-ref cannot
// say otherwise.
func (c Client) MoveSync(ctx context.Context, from, to string) error {
	if err := safeRef(from); err != nil {
		return err
	}
	if err := safeRef(to); err != nil {
		return err
	}
	remotes, err := c.remotes(ctx)
	if err != nil {
		return err
	}
	for _, remote := range remotes {
		history, err := c.SyncHistory(ctx, remote, from, maxSyncHistory)
		if err != nil {
			return err
		}
		if len(history) == 0 {
			continue
		}
		for index := len(history) - 1; index >= 0; index-- {
			if err := c.RecordSync(ctx, remote, to, history[index]); err != nil {
				return err
			}
		}
		if err := c.deleteRef(ctx, SyncedRef(remote, from)); err != nil {
			return err
		}
	}
	return nil
}

// maxSyncHistory bounds how much of a sync point's history a rename carries.
// A branch pulled and pushed through g2g a few times a day stays well inside
// it for longer than git keeps the entries.
const maxSyncHistory = 1000

func (c Client) remotes(ctx context.Context) ([]string, error) {
	output, err := c.run(ctx, "remote")
	if err != nil {
		return nil, err
	}
	return outputLines(output), nil
}

func (c Client) refExists(ctx context.Context, ref string) (bool, error) {
	if _, err := c.run(ctx, "show-ref", "--verify", "--quiet", ref); err != nil {
		if _, exited := subprocess.ExitCode(err); exited {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (c Client) reflogExists(ctx context.Context, ref string) (bool, error) {
	if _, err := c.run(ctx, "reflog", "exists", ref); err != nil {
		if _, exited := subprocess.ExitCode(err); exited {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// deleteRef removes a ref and its reflog. One already gone is not a failure.
func (c Client) deleteRef(ctx context.Context, ref string) error {
	exists, err := c.refExists(ctx, ref)
	if err != nil || !exists {
		return err
	}
	if _, err := c.run(ctx, "update-ref", "-d", ref); err != nil {
		return fmt.Errorf("could not remove %s: %w", ref, err)
	}
	return nil
}

package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/shhac/g2g/internal/worktree"
)

// WorktreeDir identifies this checkout's private Git directory, not the common
// directory shared by linked worktrees. Rebase state belongs to this directory.
func (c Client) WorktreeDir(ctx context.Context) (string, error) {
	output, err := c.run(ctx, "rev-parse", "--absolute-git-dir")
	return strings.TrimSpace(string(output)), err
}

// CheckedOutElsewhere maps each branch checked out in another worktree to the
// worktree holding it. The current worktree is excluded: rewriting the branch
// you are standing on is ordinary, and the engines handle it.
//
// A rewrite does not need to check a branch out to move it, so nothing stopped
// one from moving a branch another worktree had. Git updates the ref, that
// worktree's index and working tree still describe the old commit, and its next
// git status reports staged changes nobody made.
func (c Client) CheckedOutElsewhere(ctx context.Context) (map[string]string, error) {
	holders, err := c.HoldersElsewhere(ctx)
	if err != nil {
		return nil, err
	}
	paths := make(map[string]string, len(holders))
	for branch, holder := range holders {
		paths[branch] = holder.Path
	}
	return paths, nil
}

// HoldersElsewhere is CheckedOutElsewhere with what Git is doing there: a
// worktree part-way through a rebase or a bisect has the branch in use while
// git worktree list calls it detached.
func (c Client) HoldersElsewhere(ctx context.Context) (map[string]worktree.Holder, error) {
	holders, err := c.holders(ctx)
	if err != nil {
		return nil, err
	}
	current, err := c.run(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	here := strings.TrimSpace(string(current))
	for branch, holder := range holders {
		if holder.Path == here {
			delete(holders, branch)
		}
	}
	return holders, nil
}

// BranchHolders includes this worktree too. A deletion, unlike a rewrite,
// must refuse every checked-out branch, including the current one.
func (c Client) BranchHolders(ctx context.Context) (map[string]string, error) {
	holders, err := c.holders(ctx)
	if err != nil {
		return nil, err
	}
	paths := make(map[string]string, len(holders))
	for branch, holder := range holders {
		paths[branch] = holder.Path
	}
	return paths, nil
}

// holders reads every worktree's branch, including the one a detached
// worktree is rebasing or bisecting. Git refuses to move or delete such a
// branch ("used by worktree"), and reading only the branch lines missed it:
// the branch looked free while somebody's rebase was about to write it.
func (c Client) holders(ctx context.Context) (map[string]worktree.Holder, error) {
	output, err := c.run(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	holders := map[string]worktree.Holder{}
	var path string
	for _, line := range outputLines(output) {
		switch {
		case strings.HasPrefix(line, "worktree "):
			path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch "):
			branch := strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
			if path != "" && branch != "" {
				holders[branch] = worktree.Holder{Branch: branch, Path: path}
			}
		case line == "detached":
			// A detached worktree has no ref for a rewrite to move underneath
			// it, unless Git is part-way through something on a branch.
			if branch, busy := operating(path); branch != "" {
				holders[branch] = worktree.Holder{Branch: branch, Path: path, Busy: busy}
			}
		}
	}
	return holders, nil
}

// operating is the branch a detached worktree's rebase or bisect will write
// back to, read from the files Git keeps for it. Nothing to read is nothing
// in progress.
func operating(path string) (branch, operation string) {
	dir := gitDir(path)
	if dir == "" {
		return "", ""
	}
	for _, state := range []struct{ file, operation string }{
		{"rebase-merge/head-name", "rebase"},
		{"rebase-apply/head-name", "rebase"},
		{"BISECT_START", "bisect"},
	} {
		content, err := os.ReadFile(filepath.Join(dir, state.file))
		if err != nil {
			continue
		}
		name := strings.TrimPrefix(strings.TrimSpace(string(content)), "refs/heads/")
		// A bisect started from a detached HEAD records a commit, not a branch.
		if name != "" && name != "detached HEAD" && !isObjectID(name) {
			return name, state.operation
		}
	}
	return "", ""
}

// gitDir is a worktree's private Git directory: .git itself in the main
// worktree, and the directory a linked worktree's .git file points at.
func gitDir(path string) string {
	dotGit := filepath.Join(path, ".git")
	info, err := os.Stat(dotGit)
	if err != nil {
		return ""
	}
	if info.IsDir() {
		return dotGit
	}
	content, err := os.ReadFile(dotGit)
	if err != nil {
		return ""
	}
	dir, found := strings.CutPrefix(strings.TrimSpace(string(content)), "gitdir: ")
	if !found {
		return ""
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(path, dir)
	}
	return dir
}

func isObjectID(name string) bool {
	if len(name) != 40 && len(name) != 64 {
		return false
	}
	return strings.Trim(name, "0123456789abcdef") == ""
}

// WorktreeClean reports a worktree with nothing uncommitted, untracked files
// included. Moving a clean worktree to another commit loses nothing; an
// untracked file is counted because one the new commit also has is exactly
// what would stop it.
func (c Client) WorktreeClean(ctx context.Context, path string) (bool, error) {
	output, err := c.run(ctx, "-C", path, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(output)) == "", nil
}

// SwitchTreeIn is SwitchTree in another worktree: after its branch has moved,
// bring that worktree's index and files from the commit they describe to the
// one the branch now points at.
func (c Client) SwitchTreeIn(ctx context.Context, path, from, to string) error {
	if err := safeRef(from); err != nil {
		return err
	}
	if err := safeRef(to); err != nil {
		return err
	}
	// Stat data the other worktree has not refreshed reads as a change to
	// read-tree, which then refuses a tree that is in fact clean. Refreshing
	// fails when something really has changed; read-tree is what says so.
	_, _ = c.run(ctx, "-C", path, "update-index", "-q", "--refresh")
	_, err := c.run(ctx, "-C", path, "read-tree", "-m", "-u", from, to)
	return err
}

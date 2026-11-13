package restack

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Whose a journal is, and where that worktree is now. It goes by the file a
// record is in, never by the absolute path inside it, which a moved
// repository leaves stale, and Git's layout of linked worktrees is what says
// where each one is.

// WorktreeLocator identifies the private Git directory owning rebase state.
type WorktreeLocator interface {
	WorktreeDir(context.Context) (string, error)
}

// journalFiles is where every journal is, and which worktree is asking.
type journalFiles struct {
	common, shared, linked string
	// name is the asking worktree's, as Git names its private directory:
	// empty for the main worktree. located is false for a Git that cannot
	// say which worktree it is, which shares one file among all of them.
	name    string
	located bool
}

// journalEntry is one journal file and the record in it.
type journalEntry struct {
	path   string
	record Record
}

func (j FileJournal) locate(ctx context.Context) (journalFiles, error) {
	if j.Git == nil {
		return journalFiles{}, fmt.Errorf("restack journal is not configured")
	}
	common, err := j.Git.CommonDir(ctx)
	if err != nil {
		return journalFiles{}, err
	}
	files := journalFiles{
		common: common,
		shared: filepath.Join(common, "g2g", journalFileName),
		linked: filepath.Join(common, "g2g", "restack", "worktrees"),
	}
	locator, ok := j.Git.(WorktreeLocator)
	if !ok {
		return files, nil
	}
	dir, err := locator.WorktreeDir(ctx)
	if err != nil {
		return journalFiles{}, err
	}
	files.located = true
	if filepath.Clean(dir) != filepath.Clean(common) {
		files.name = filepath.Base(dir)
	}
	return files, nil
}

// own is where the asking worktree's journal goes when it has none yet.
func (f journalFiles) own() string {
	if f.name == "" {
		return f.shared
	}
	return filepath.Join(f.linked, f.name+".json")
}

// owner is the worktree whose record an entry is, by the file it is in, or
// anyone's for a record in the shared file that names no worktree.
func (f journalFiles) owner(entry journalEntry) (name string, anyone bool) {
	if entry.path != f.shared {
		return strings.TrimSuffix(filepath.Base(entry.path), ".json"), false
	}
	if entry.record.Worktree == "" {
		return "", true
	}
	return f.linkedName(entry.record.Worktree), false
}

// linkedName is the name of the linked worktree a private Git directory
// belongs to, which Git keeps under <common>/worktrees/<name>, or empty for
// the main worktree's. The common directory itself is the main worktree's
// whatever its checkout is called, and a name of .git is a main worktree's
// directory, never a linked one: a checkout that is itself named worktrees
// read as a linked worktree called .git, which then read as gone.
func (f journalFiles) linkedName(gitDir string) string {
	dir := filepath.Clean(gitDir)
	if dir == filepath.Clean(f.common) || filepath.Base(dir) == ".git" || filepath.Base(filepath.Dir(dir)) != "worktrees" {
		return ""
	}
	return filepath.Base(dir)
}

// mine reports an entry that is the asking worktree's.
func (f journalFiles) mine(entry journalEntry) bool {
	name, anyone := f.owner(entry)
	return !f.located || anyone || name == f.name
}

// read is the entries among paths that exist. A record that does not parse
// is an error: it is somebody's rollback state, and guessing past it could
// move what it was about to put back.
func read(paths ...string) ([]journalEntry, error) {
	entries := make([]journalEntry, 0, len(paths))
	for _, path := range paths {
		record, found, err := readJournal(path)
		if err != nil {
			return nil, err
		}
		if found {
			entries = append(entries, journalEntry{path: path, record: record})
		}
	}
	return entries, nil
}

// asking is the asking worktree's entry, if it has one: in its own file, or,
// for a linked worktree, in the shared file an older g2g wrote it to. The
// shared file is read only when its own holds nothing, so another
// worktree's damaged journal stops nobody's own restack.
func (f journalFiles) asking() (journalEntry, bool, error) {
	candidates := []string{f.own()}
	if f.own() != f.shared {
		candidates = append(candidates, f.shared)
	}
	for _, path := range candidates {
		entries, err := read(path)
		if err != nil {
			return journalEntry{}, false, err
		}
		if len(entries) != 0 && f.mine(entries[0]) {
			return entries[0], true, nil
		}
	}
	return journalEntry{}, false, nil
}

// others is every other worktree's entry, with where that worktree is now.
// The linked journals are listed by reading their directory: a pattern built
// from the repository's path read any bracket in it as a pattern, and saw no
// journal at all.
func (f journalFiles) others() ([]Elsewhere, error) {
	listed, err := os.ReadDir(f.linked)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("list restack journals: %w", err)
	}
	paths := []string{f.shared}
	for _, file := range listed {
		if !file.IsDir() && filepath.Ext(file.Name()) == ".json" {
			paths = append(paths, filepath.Join(f.linked, file.Name()))
		}
	}
	entries, err := read(paths...)
	if err != nil {
		return nil, err
	}
	others := make([]Elsewhere, 0)
	for _, entry := range entries {
		if !f.mine(entry) {
			others = append(others, f.elsewhere(entry))
		}
	}
	return others, nil
}

// elsewhere is another worktree's entry, with where that worktree is now.
// It is gone only when Git no longer has its private directory, which is
// what removing a worktree takes; a checkout that cannot be found is not
// evidence that nobody is in the middle of it.
func (f journalFiles) elsewhere(entry journalEntry) Elsewhere {
	found := Elsewhere{Record: entry.record, Path: entry.path}
	name, anyone := f.owner(entry)
	if anyone {
		return found
	}
	if name == "" {
		found.GitDir = f.common
		if filepath.Base(f.common) == ".git" {
			found.Checkout = filepath.Dir(f.common)
		}
		return found
	}
	found.GitDir = filepath.Join(f.common, "worktrees", name)
	if _, err := os.Stat(found.GitDir); errors.Is(err, fs.ErrNotExist) {
		found.Gone = true
		return found
	}
	found.Checkout = backlinkedCheckout(found.GitDir)
	return found
}

// backlinkedCheckout is the checkout a linked worktree's private directory
// names in its gitdir file, absolute or relative to that directory as Git
// may write it, or empty when it names none that exists.
func backlinkedCheckout(gitDir string) string {
	data, err := os.ReadFile(filepath.Join(gitDir, "gitdir"))
	if err != nil {
		return ""
	}
	path := strings.TrimSpace(string(data))
	if !filepath.IsAbs(path) {
		path = filepath.Join(gitDir, path)
	}
	if filepath.Base(path) != ".git" {
		return ""
	}
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return filepath.Dir(filepath.Clean(path))
}

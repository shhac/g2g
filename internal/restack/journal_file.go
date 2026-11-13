package restack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/shhac/g2g/internal/graph"
)

const journalFileName = "restack.json"

// FileJournal keeps each worktree's record beside the graph, under the Git
// common directory: the main worktree's in g2g/restack.json, and a linked
// worktree's in g2g/restack/worktrees/<name>.json, after the name Git gives
// its private directory.
//
// One file for every worktree made one restack stopped on a conflict refuse
// every other restack in every other worktree, on stacks with nothing in
// common with it, and named a --continue and an --abort that belonged to
// whoever started it. Each record still lives under the common directory
// rather than in the worktree's own private directory, because it is how the
// shared refs it moved are put back: removing a worktree must not quietly
// take its rollback record with it.
//
// Whose a record is goes by the file it is in, never by the absolute path
// inside it, which a moved repository leaves stale. The main worktree's file
// is also the one every worktree shared before, so a record there that names
// a linked worktree is that worktree's, and one that names none is anyone's,
// as it was then.
type FileJournal struct {
	Git graph.Locator
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
	return linkedName(entry.record.Worktree), false
}

// linkedName is the name of the linked worktree a private Git directory
// belongs to, which Git keeps under <common>/worktrees/, or empty for the
// main worktree's.
func linkedName(gitDir string) string {
	if filepath.Base(filepath.Dir(filepath.Clean(gitDir))) != "worktrees" {
		return ""
	}
	return filepath.Base(gitDir)
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

// asking is the asking worktree's entry, if it has one. Only the files that
// can hold it are read, so another worktree's damaged journal stops nobody
// but the guards that would have weighed it.
func (f journalFiles) asking() (journalEntry, bool, error) {
	entries, err := read(f.own(), f.shared)
	if err != nil {
		return journalEntry{}, false, err
	}
	for _, entry := range entries {
		if f.mine(entry) {
			return entry, true, nil
		}
	}
	return journalEntry{}, false, nil
}

// Load returns this worktree's in-flight record. Absent is the ordinary
// state and not an error.
func (j FileJournal) Load(ctx context.Context) (Record, bool, error) {
	files, err := j.locate(ctx)
	if err != nil {
		return Record{}, false, err
	}
	entry, found, err := files.asking()
	return entry.record, found, err
}

// Unfinished is this worktree's in-flight record and every other worktree's,
// whether or not that worktree still exists: a record outlives its checkout,
// because the refs it moved are shared.
func (j FileJournal) Unfinished(ctx context.Context) (*Record, []Elsewhere, error) {
	files, err := j.locate(ctx)
	if err != nil {
		return nil, nil, err
	}
	linked, err := filepath.Glob(filepath.Join(files.linked, "*.json"))
	if err != nil {
		return nil, nil, err
	}
	entries, err := read(append([]string{files.shared}, linked...)...)
	if err != nil {
		return nil, nil, err
	}
	var here *Record
	others := make([]Elsewhere, 0)
	for _, entry := range entries {
		if files.mine(entry) {
			record := entry.record
			here = &record
			continue
		}
		others = append(others, files.elsewhere(entry))
	}
	return here, others, nil
}

// elsewhere is another worktree's entry, with where that worktree is now.
// It is gone only when Git no longer has its private directory, which is
// what removing a worktree takes; a checkout that cannot be found is not
// evidence that nobody is in the middle of it.
func (f journalFiles) elsewhere(entry journalEntry) Elsewhere {
	found := Elsewhere{Record: entry.record, Path: entry.path}
	name, anyone := f.owner(entry)
	switch {
	case anyone:
	case name == "":
		found.GitDir = f.common
		if filepath.Base(f.common) == ".git" {
			found.Checkout = filepath.Dir(f.common)
		}
	default:
		found.GitDir = filepath.Join(f.common, "worktrees", name)
		if _, err := os.Stat(found.GitDir); errors.Is(err, fs.ErrNotExist) {
			found.Gone = true
			break
		}
		found.Checkout = backlinkedCheckout(found.GitDir)
	}
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

// holding is the file this worktree's record is in, or the one a new record
// goes in.
func (j FileJournal) holding(ctx context.Context) (string, bool, error) {
	files, err := j.locate(ctx)
	if err != nil {
		return "", false, err
	}
	entry, found, err := files.asking()
	if err != nil || !found {
		return files.own(), false, err
	}
	return entry.path, true, nil
}

// readJournal reads one journal file. Absent is not an error.
func readJournal(path string) (Record, bool, error) {
	contents, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, fmt.Errorf("read restack journal: %w", err)
	}
	var record Record
	if err := json.Unmarshal(contents, &record); err != nil {
		return Record{}, false, fmt.Errorf("parse restack journal %s: %w", path, err)
	}
	if record.SchemaVersion != 1 && record.SchemaVersion != JournalSchemaVersion {
		return Record{}, false, fmt.Errorf("restack journal %s has schema version %d, which this g2g does not support (expected %d)", path, record.SchemaVersion, JournalSchemaVersion)
	}
	if record.SchemaVersion == 1 && record.Above != "" {
		return Record{}, false, fmt.Errorf("restack journal %s has a replay boundary without schema version 2", path)
	}
	return record, true, nil
}

// Save writes the record atomically, so an interrupted write cannot leave a
// half-parsed journal that blocks every later command. It goes where this
// worktree's record already is, which for a restack an older g2g started is
// the shared file, until it is finished.
func (j FileJournal) Save(ctx context.Context, record Record) error {
	path, _, err := j.holding(ctx)
	if err != nil {
		return err
	}
	return write(path, record, false)
}

// Start publishes a new journal only if none exists. Linking a complete
// temporary file is atomic and exclusive, unlike check-then-rename; two
// restacks starting at once cannot replace each other's recovery state.
func (j FileJournal) Start(ctx context.Context, record Record) error {
	files, err := j.locate(ctx)
	if err != nil {
		return err
	}
	return write(files.own(), record, true)
}

func write(path string, record Record, exclusive bool) error {
	record.SchemaVersion = 1
	if record.Above != "" {
		record.SchemaVersion = JournalSchemaVersion
	}
	contents, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("encode restack journal: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create restack journal directory: %w", err)
	}
	temporary, err := os.CreateTemp(dir, journalFileName+".*")
	if err != nil {
		return fmt.Errorf("create restack journal temporary file: %w", err)
	}
	name := temporary.Name()
	if _, err := temporary.Write(append(contents, '\n')); err != nil {
		temporary.Close()
		os.Remove(name)
		return fmt.Errorf("write restack journal: %w", err)
	}
	// Flush to disk before it is installed, as the graph store does: this is
	// the record a crash is recovered from, and a crash must not leave its name
	// pointing at an empty file.
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		os.Remove(name)
		return fmt.Errorf("flush restack journal: %w", err)
	}
	if err := temporary.Close(); err != nil {
		os.Remove(name)
		return fmt.Errorf("close restack journal: %w", err)
	}
	install := os.Rename
	if exclusive {
		install = os.Link
	}
	if err := install(name, path); err != nil {
		os.Remove(name)
		if exclusive && errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("a restack is already in progress · run g2g restack --continue or g2g restack --abort in the worktree that started it")
		}
		return fmt.Errorf("replace restack journal: %w", err)
	}
	if exclusive {
		os.Remove(name)
	}
	return nil
}

// Clear removes this worktree's record. Clearing when nothing is in flight is
// harmless, because finishing an operation that was never journalled is
// ordinary, and another worktree's record in the shared file is never this
// worktree's to clear. A record that does not parse is cleared from where
// this worktree's would be, since nothing else can.
func (j FileJournal) Clear(ctx context.Context) error {
	path, found, err := j.holding(ctx)
	if err != nil {
		files, locateErr := j.locate(ctx)
		if locateErr != nil {
			return locateErr
		}
		path, found = files.own(), true
	}
	if !found {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove restack journal: %w", err)
	}
	return nil
}

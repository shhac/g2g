package restack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

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

// Elsewhere is every unfinished restack another worktree owns, whether or
// not that worktree still exists: a record outlives its checkout, because the
// refs it moved are shared.
func (j FileJournal) Elsewhere(ctx context.Context) ([]Elsewhere, error) {
	files, err := j.locate(ctx)
	if err != nil {
		return nil, err
	}
	return files.others()
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
//
// The file can be taken by another worktree's restack only where every
// worktree shared one: a restack an older g2g started in a linked worktree.
// That refusal names it, as every other refusal over another worktree does.
func (j FileJournal) Start(ctx context.Context, record Record) error {
	files, err := j.locate(ctx)
	if err != nil {
		return err
	}
	err = write(files.own(), record, true)
	if !errors.Is(err, fs.ErrExist) {
		return err
	}
	entries, readErr := read(files.own())
	if readErr != nil || len(entries) == 0 {
		return errors.Join(err, readErr)
	}
	if files.mine(entries[0]) {
		return busy(entries[0].record)
	}
	return busyElsewhere(files.elsewhere(entries[0]), nil)
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
			return fs.ErrExist
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
	files, err := j.locate(ctx)
	if err != nil {
		return err
	}
	path := files.own()
	if entry, found, err := files.asking(); err == nil {
		if !found {
			return nil
		}
		path = entry.path
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove restack journal: %w", err)
	}
	return nil
}

// Package restack rewrites a stack's contents so they match its recorded
// structure.
//
// It is gt2gh's only resumable operation. Everything else is one-shot:
// preview, apply, done. A rewrite can stop half-way on a conflict that only a
// person can resolve, so it leaves a record behind and every other command has
// to notice.
package restack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/shhac/gt2gh/internal/graph"
)

// JournalSchemaVersion is bumped when a recorded field changes meaning. It is
// its own namespace, separate from the graph store's and from the CLI's
// output schema.
const JournalSchemaVersion = 1

const journalFileName = "restack.json"

// Record is what survives between invocations of one restack.
//
// It deliberately does not hold the remaining queue. git already journals the
// rebase it is part-way through, and the work still outstanding is re-derived
// from the refs every time — which is what makes a user's own git rebase
// --continue or --abort harmless rather than something to detect.
//
// What git cannot supply is Original: it restores only the invocation it is
// running, so rolling back paths that already completed needs our own record.
type Record struct {
	SchemaVersion int `json:"schemaVersion"`
	// Onto is an explicit --onto target, empty when the structure already says
	// where each branch belongs.
	Onto string `json:"onto,omitempty"`
	// Absorb records whether the user chose to keep commits their parent
	// dropped, so continuing does not silently change that decision.
	Absorb bool `json:"absorb,omitempty"`
	// Branch and Scope are the selection, so continuing covers the same set.
	Branch string `json:"branch,omitempty"`
	Scope  string `json:"scope,omitempty"`
	// ReturnTo is the branch the user was on, restored when the work finishes.
	ReturnTo string `json:"returnTo,omitempty"`
	// Original maps every branch in the operation to its tip when the
	// operation began. This is the whole reason for --abort.
	Original map[string]string `json:"original"`
}

// Selection rebuilds the graph selection this record was started with.
func (r Record) Selection() graph.Selection {
	return graph.Selection{Branch: r.Branch, Scope: graph.Scope(r.Scope)}
}

// Journal stores the record for an in-flight restack.
type Journal interface {
	Load(context.Context) (Record, bool, error)
	Save(context.Context, Record) error
	Clear(context.Context) error
}

// FileJournal keeps the record beside the graph, under the Git common
// directory, so linked worktrees see the same in-flight operation.
type FileJournal struct {
	Git graph.Locator
}

func (j FileJournal) path(ctx context.Context) (string, error) {
	if j.Git == nil {
		return "", fmt.Errorf("restack journal is not configured")
	}
	common, err := j.Git.CommonDir(ctx)
	if err != nil {
		return "", err
	}
	return filepath.Join(common, "g2g", journalFileName), nil
}

// Load returns the in-flight record. Absent is the ordinary state and not an
// error.
func (j FileJournal) Load(ctx context.Context) (Record, bool, error) {
	path, err := j.path(ctx)
	if err != nil {
		return Record{}, false, err
	}
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
	if record.SchemaVersion != JournalSchemaVersion {
		return Record{}, false, fmt.Errorf("restack journal %s has schema version %d, which this gt2gh does not support (expected %d)", path, record.SchemaVersion, JournalSchemaVersion)
	}
	return record, true, nil
}

// Save writes the record atomically, so an interrupted write cannot leave a
// half-parsed journal that blocks every later command.
func (j FileJournal) Save(ctx context.Context, record Record) error {
	path, err := j.path(ctx)
	if err != nil {
		return err
	}
	record.SchemaVersion = JournalSchemaVersion
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
	if err := temporary.Close(); err != nil {
		os.Remove(name)
		return fmt.Errorf("close restack journal: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return fmt.Errorf("replace restack journal: %w", err)
	}
	return nil
}

// Clear removes the record. Clearing when nothing is in flight is harmless,
// because finishing an operation that was never journalled is ordinary.
func (j FileJournal) Clear(ctx context.Context) error {
	path, err := j.path(ctx)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove restack journal: %w", err)
	}
	return nil
}

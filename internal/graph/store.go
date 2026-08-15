package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// StoreSchemaVersion is bumped when a stored field changes meaning or
// disappears. Adding a field is not a breaking change and does not bump it.
//
// This is deliberately a different name from the CLI's output schemaVersion.
// The two evolve independently and must never be reasoned about as one number.
const StoreSchemaVersion = 1

// storeDirName is the subdirectory of the Git common directory that holds the
// graph. Keeping it out of the common directory's root leaves room for other
// gt2gh state without colliding with Git's own names.
const storeDirName = "g2g"

// storeFileName is the whole store: one flat file, because graph identity is
// derived rather than recorded and there is nothing to shard on.
const storeFileName = "graph.json"

// Locator supplies the Git common directory. Linked worktrees share it, so a
// graph adopted in one worktree is visible from its siblings.
type Locator interface {
	CommonDir(context.Context) (string, error)
}

// Store reads and writes the adopted graph.
type Store interface {
	Load(context.Context) (Graph, error)
	Save(context.Context, Graph) error
}

// FileStore keeps the graph in a directory under the Git common directory.
//
// That location is chosen so the store is shared between linked worktrees,
// never appears in a diff, never dirties a checkout, and does not collide with
// the clean-worktree precondition every mutation depends on. It is
// intentionally not shared between clones and never pushed: a fresh clone
// starts empty, which is consistent, because the unpublished branches these
// edges describe do not survive a clone either.
type FileStore struct {
	Git Locator
}

func (s FileStore) dir(ctx context.Context) (string, error) {
	if s.Git == nil {
		return "", fmt.Errorf("graph store is not configured")
	}
	common, err := s.Git.CommonDir(ctx)
	if err != nil {
		return "", err
	}
	return filepath.Join(common, storeDirName), nil
}

// Path reports where the graph is kept, so a preview can name the file it is
// about to write rather than describing it.
func (s FileStore) Path(ctx context.Context) (string, error) {
	dir, err := s.dir(ctx)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, storeFileName), nil
}

// document is the on-disk shape. Branch edges are a map rather than a list so
// a branch cannot appear twice with different parents.
type document struct {
	StoreSchemaVersion int                   `json:"storeSchemaVersion"`
	Trunks             []string              `json:"trunks,omitempty"`
	Branches           map[string]storedEdge `json:"branches"`
}

type storedEdge struct {
	Parent    string `json:"parent"`
	Authority string `json:"authority"`
	Origin    string `json:"origin,omitempty"`
}

// Load returns the adopted graph. A missing store is an empty graph rather
// than an error: not having adopted anything yet is the normal starting state.
func (s FileStore) Load(ctx context.Context) (Graph, error) {
	path, err := s.Path(ctx)
	if err != nil {
		return Graph{}, err
	}
	contents, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return New(), nil
	}
	if err != nil {
		return Graph{}, fmt.Errorf("read graph store: %w", err)
	}
	return decode(contents, path)
}

func decode(contents []byte, path string) (Graph, error) {
	var doc document
	if err := json.Unmarshal(contents, &doc); err != nil {
		return Graph{}, fmt.Errorf("parse graph store %s: %w", path, err)
	}
	// Fail closed on a version this build does not know. Reading a future
	// store optimistically is how a newer gt2gh's structure gets silently
	// rewritten by an older one.
	if doc.StoreSchemaVersion != StoreSchemaVersion {
		return Graph{}, fmt.Errorf("graph store %s has schema version %d, which this gt2gh does not support (expected %d)", path, doc.StoreSchemaVersion, StoreSchemaVersion)
	}
	loaded := Graph{Edges: make(map[string]Edge, len(doc.Branches)), Trunks: doc.Trunks}
	for branch, stored := range doc.Branches {
		loaded.Edges[branch] = Edge{
			Parent:    stored.Parent,
			Authority: Authority(stored.Authority),
			Origin:    Origin(stored.Origin),
		}
	}
	if err := loaded.Validate(); err != nil {
		return Graph{}, fmt.Errorf("graph store %s is not a forest: %w", path, err)
	}
	return loaded, nil
}

// Save writes the graph atomically: a temporary file in the same directory,
// then a rename. A concurrent reader therefore sees either the previous graph
// or the new one and never a half-written file.
//
// Concurrent writers are last-writer-wins. The store is small, is written only
// by an explicit --apply, and each write is preceded by a revalidation that
// compares what was previewed, so a lock would add a failure mode without
// removing one.
func (s FileStore) Save(ctx context.Context, g Graph) error {
	if err := g.Validate(); err != nil {
		return err
	}
	dir, err := s.dir(ctx)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create graph store directory: %w", err)
	}
	contents, err := encode(g)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(dir, storeFileName+".*")
	if err != nil {
		return fmt.Errorf("create graph store temporary file: %w", err)
	}
	name := temporary.Name()
	if err := writeAndClose(temporary, contents); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, filepath.Join(dir, storeFileName)); err != nil {
		os.Remove(name)
		return fmt.Errorf("replace graph store: %w", err)
	}
	return nil
}

func writeAndClose(file *os.File, contents []byte) error {
	if _, err := file.Write(contents); err != nil {
		file.Close()
		return fmt.Errorf("write graph store: %w", err)
	}
	// Flush to disk before the rename, so a crash cannot leave the new name
	// pointing at an empty file.
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("flush graph store: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close graph store: %w", err)
	}
	return nil
}

func encode(g Graph) ([]byte, error) {
	doc := document{
		StoreSchemaVersion: StoreSchemaVersion,
		Trunks:             g.Trunks,
		Branches:           make(map[string]storedEdge, len(g.Edges)),
	}
	for branch, edge := range g.Edges {
		doc.Branches[branch] = storedEdge{
			Parent:    edge.Parent,
			Authority: string(edge.Authority),
			Origin:    string(edge.Origin),
		}
	}
	contents, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode graph store: %w", err)
	}
	return append(contents, '\n'), nil
}

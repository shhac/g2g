// Package snapshot connects a preview to the apply that follows it.
//
// A preview and an apply are two runs with nothing between them: the apply
// plans again, so the plan a person read is never the plan that is applied,
// only one that came out the same if nothing moved. A snapshot is the plan
// written down with what it rests on. An apply given one checks that those
// things are still so and carries out that plan, without planning.
//
// What a plan rests on is named by the command, and is deliberately less than
// "the repository". A repository several sessions share is never still, and a
// check that refused whenever anything anywhere moved would refuse almost
// always on the repositories where planning is slow enough to want this. See
// design-docs/snapshots.md.
package snapshot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/shhac/g2g/internal/graph"
)

// Rests is what a plan rests on: the things that, if they are as they were,
// make the plan as true as when it was made.
type Rests struct {
	// Branches are compared exactly: the tip, the edge the graph records, the
	// edges recorded on it, and the worktree that holds it.
	Branches []string
	// Advancing are branches the plan needs only to still contain what they
	// contained, such as a trunk branches were found landed in.
	Advancing []string
	// Here says the plan depends on where this checkout stands.
	Here bool
}

// World is the part of a repository a basis is cut from, read once.
type World struct {
	Tips    map[string]string
	Graph   graph.Graph
	Holders map[string]string
	Here    string
}

// Reader is the Git a World is read from.
type Reader interface {
	LocalTips(ctx context.Context) (map[string]string, error)
	BranchHolders(ctx context.Context) (map[string]string, error)
	Here(ctx context.Context) (string, error)
}

// Read reads the world as it is now: a few local reads, whatever a plan made
// from it costs.
func Read(ctx context.Context, git Reader, store graph.Store) (World, error) {
	tips, err := git.LocalTips(ctx)
	if err != nil {
		return World{}, err
	}
	holders, err := git.BranchHolders(ctx)
	if err != nil {
		return World{}, err
	}
	here, err := git.Here(ctx)
	if err != nil {
		return World{}, err
	}
	recorded, err := store.Load(ctx)
	if err != nil {
		return World{}, err
	}
	return World{Tips: tips, Graph: recorded, Holders: holders, Here: here}, nil
}

// Basis is what a plan rested on, as the world was when it was made.
type Basis struct {
	Tips      map[string]string     `json:"tips"`
	Advancing map[string]string     `json:"advancing,omitempty"`
	Edges     map[string]graph.Edge `json:"edges"`
	Children  map[string][]string   `json:"children"`
	Holders   map[string]string     `json:"holders"`
	Here      *string               `json:"here,omitempty"`
}

// Basis cuts from the world what a plan rests on.
func (w World) Basis(rests Rests) Basis {
	basis := Basis{
		Tips: map[string]string{}, Edges: map[string]graph.Edge{},
		Children: map[string][]string{}, Holders: map[string]string{},
	}
	for _, branch := range rests.Branches {
		if branch == "" {
			continue
		}
		basis.Tips[branch] = w.Tips[branch]
		basis.Edges[branch] = w.Graph.Edges[branch]
		basis.Children[branch] = w.Graph.Children(branch)
		basis.Holders[branch] = w.Holders[branch]
	}
	for _, branch := range rests.Advancing {
		if _, exact := basis.Tips[branch]; exact || branch == "" {
			continue
		}
		if basis.Advancing == nil {
			basis.Advancing = map[string]string{}
		}
		basis.Advancing[branch] = w.Tips[branch]
	}
	if rests.Here {
		basis.Here = &w.Here
	}
	return basis
}

// Seen moves each advancing branch to where a later reading has it. A plan is
// made between two readings, and an advancing branch may have moved between
// them: the plan may then have judged against any commit up to the later one,
// so that is the one that has to stay contained. Recording the earlier let a
// trunk be wound back past what a branch had been found landed in.
func (b Basis) Seen(later World) Basis {
	for branch := range b.Advancing {
		b.Advancing[branch] = later.Tips[branch]
	}
	return b
}

// rests is what this basis was cut for, read back from it.
func (b Basis) rests() Rests {
	return Rests{
		Branches:  slices.Sorted(maps.Keys(b.Tips)),
		Advancing: slices.Sorted(maps.Keys(b.Advancing)),
		Here:      b.Here != nil,
	}
}

// Ancestry answers whether one commit contains another.
type Ancestry interface {
	IsAncestor(ctx context.Context, ancestor, descendant string) (bool, error)
}

// Changed names the first thing a plan rested on that is no longer so in the
// world as it is now, empty when everything still is.
func (b Basis) Changed(ctx context.Context, git Ancestry, now World) (string, error) {
	current := now.Basis(b.rests())
	for _, branch := range slices.Sorted(maps.Keys(b.Tips)) {
		was, is := b.Tips[branch], current.Tips[branch]
		switch {
		case was == is:
		case is == "":
			return branch + " no longer exists", nil
		case was == "":
			return branch + " exists now and did not", nil
		default:
			return fmt.Sprintf("%s moved from %s to %s", branch, short(was), short(is)), nil
		}
		if b.Edges[branch] != current.Edges[branch] {
			return "what the graph records of " + branch + " changed", nil
		}
		if !slices.Equal(b.Children[branch], current.Children[branch]) {
			return "the branches recorded on " + branch + " changed", nil
		}
		if was, is := b.Holders[branch], current.Holders[branch]; was != is {
			if is == "" {
				return branch + " is no longer checked out in " + was, nil
			}
			return branch + " is checked out in " + is, nil
		}
	}
	for _, branch := range slices.Sorted(maps.Keys(b.Advancing)) {
		was, is := b.Advancing[branch], current.Advancing[branch]
		if was == is {
			continue
		}
		if was == "" || is == "" {
			return branch + " was created or deleted", nil
		}
		contains, err := git.IsAncestor(ctx, was, is)
		if err != nil {
			return "", err
		}
		if !contains {
			return fmt.Sprintf("%s was rewritten: it no longer contains %s", branch, short(was)), nil
		}
	}
	if b.Here != nil && *b.Here != *current.Here {
		return fmt.Sprintf("this checkout moved from %s to %s", *b.Here, *current.Here), nil
	}
	return "", nil
}

func short(object string) string {
	if len(object) <= 12 {
		return object
	}
	return object[:12]
}

// format is the layout of the file. A file in another layout is refused
// rather than read as best it can be.
const format = 1

// File is a snapshot as it is written: a plan, the request it answers, and
// what it rests on.
type File struct {
	Format int `json:"format"`
	// Tool is the g2g that made the plan. Another version may decide
	// differently, and its plan is not this one's to apply.
	Tool string `json:"tool"`
	// Command and Request say what was asked, so a plan made for one
	// selection is not applied to another because the path was reused.
	Command string          `json:"command"`
	Request []string        `json:"request"`
	Basis   Basis           `json:"basis"`
	Plan    json.RawMessage `json:"plan"`
}

// Save writes the file whole or not at all, readable only by its owner: a
// plan names branches and commits, and a half-written one must never be
// applied.
func Save(path string, file File) error {
	file.Format = format
	content, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".g2g-snapshot-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if _, err := temporary.Write(append(content, '\n')); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), path)
}

// Writable refuses, before anything is planned, a path a snapshot could not
// be written to or would replace something else at. Planning can take
// minutes, and --snapshot naming a file that is not a snapshot is far more
// likely a slip than a wish to lose that file.
func Writable(path string) error {
	if info, err := os.Stat(filepath.Dir(path)); err != nil || !info.IsDir() {
		return fmt.Errorf("a snapshot cannot be written to %s: its directory does not exist", path)
	}
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if _, err := Load(path); err != nil {
		return fmt.Errorf("%s exists and is not a g2g snapshot, so it is not replaced · name another path", path)
	}
	return nil
}

// Discard removes a snapshot a preview has nothing to replace with, so the
// plan it held is not applied in place of the one just read. Anything else at
// the path is left.
func Discard(path string) {
	if _, err := Load(path); err == nil {
		_ = os.Remove(path)
	}
}

// Load reads a snapshot, refusing one this build did not lay out.
func Load(path string) (File, error) {
	content, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return File{}, fmt.Errorf("no snapshot at %s · preview with --snapshot to write one", path)
	}
	if err != nil {
		return File{}, err
	}
	var file File
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		return File{}, fmt.Errorf("%s is not a snapshot g2g can read: %w", path, err)
	}
	if file.Format != format {
		return File{}, fmt.Errorf("%s is a snapshot in a layout this g2g does not read · preview again", path)
	}
	return file, nil
}

// Answers checks the snapshot was made by this g2g, for this command and this
// request.
func (f File) Answers(tool, command string, request []string) error {
	switch {
	case f.Tool != tool:
		return fmt.Errorf("the snapshot was made by g2g %s and this is %s · preview again", f.Tool, tool)
	case f.Command != command:
		return fmt.Errorf("the snapshot is a plan for %s, not %s", f.Command, command)
	case !slices.Equal(f.Request, request):
		return fmt.Errorf("the snapshot was made for a different request (%v, not %v) · preview again, or apply with the flags it was made with", f.Request, request)
	}
	return nil
}

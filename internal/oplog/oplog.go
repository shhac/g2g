// Package oplog records what each local operation changed, so that the last
// one can be put back.
//
// A restack that stops part-way has a journal and --abort. One that finishes
// had nothing: the journal is deleted on success, and changing your mind
// meant finding each branch's old tip in Git's reflog and repairing a record
// whose fork points had been rewritten to match commits the branches no
// longer had. An entry here is the difference between the repository before
// an operation and after it, which is the same for every command and leaves
// none of them a thing to forget to say. See design-docs/undo.md.
package oplog

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/shhac/g2g/internal/graph"
)

// Reading is the part of a repository an operation can change, read once.
type Reading struct {
	// Refs are the local branches and g2g's own refs, by full name. The
	// fetched copies of a remote are not among them: they record the remote,
	// and nothing done here.
	Refs  map[string]string
	Graph graph.Graph
	Here  string
}

// Reader is the Git a Reading is read from.
type Reader interface {
	OwnRefs(ctx context.Context) (map[string]string, error)
	Here(ctx context.Context) (string, error)
}

// Read reads the repository as it is now.
func Read(ctx context.Context, git Reader, store graph.Store) (Reading, error) {
	refs, err := git.OwnRefs(ctx)
	if err != nil {
		return Reading{}, err
	}
	here, err := git.Here(ctx)
	if err != nil {
		return Reading{}, err
	}
	recorded, err := store.Load(ctx)
	if err != nil {
		return Reading{}, err
	}
	return Reading{Refs: refs, Graph: recorded, Here: here}, nil
}

// Move is something that changed, as it was and as it became. Empty means it
// did not exist.
type Move struct {
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

// Recorded is what the graph said of one branch, before and after. A nil
// side is a branch the graph did not record then.
type Recorded struct {
	Before *graph.Edge `json:"before,omitempty"`
	After  *graph.Edge `json:"after,omitempty"`
}

// Roots is the graph's trunks and declarations, kept whole on the rare
// occasion an operation changes them.
type Roots struct {
	Trunks   []string                     `json:"trunks"`
	Declared map[string]graph.Declaration `json:"declared,omitempty"`
}

// Entry is one operation: what was run, and everything it changed.
type Entry struct {
	At       time.Time `json:"at"`
	Command  string    `json:"command"`
	Worktree string    `json:"worktree,omitempty"`
	// Refs are the refs it moved, created or deleted.
	Refs map[string]Move `json:"refs,omitempty"`
	// Edges are the branches whose record it changed.
	Edges map[string]Recorded `json:"edges,omitempty"`
	// Tips are where the branches it touched stood once it was done, those
	// it did not move as well as those it did. A branch whose record is put
	// back has to be the branch that record was about: one rebased with
	// plain Git since would get back a fork point it no longer holds.
	Tips map[string]string `json:"tips,omitempty"`
	// RootsBefore and RootsAfter are set only when it changed which
	// branches are trunks.
	RootsBefore *Roots `json:"rootsBefore,omitempty"`
	RootsAfter  *Roots `json:"rootsAfter,omitempty"`
	// Here is where the checkout stood, when the operation moved it.
	Here *Move `json:"here,omitempty"`
	// Undone marks an entry that has been put back, so the one before it is
	// the next to offer.
	Undone bool `json:"undone,omitempty"`
	// Published marks an operation that sent branches to a remote. It is
	// recorded so that an undo knows it happened, and is never itself undone:
	// what other people have seen is not put back from here.
	Published []string `json:"published,omitempty"`
}

// Empty reports an operation that changed nothing an undo could put back.
func (e Entry) Empty() bool {
	return len(e.Refs) == 0 && len(e.Edges) == 0 && e.RootsBefore == nil && e.Here == nil && len(e.Published) == 0
}

// BranchesOf is the branches a ref may be about: a local branch, the pin of
// its fork point, or its sync point with a remote. A sync point is named for
// a remote and then a branch, and either may hold a slash, so every way of
// dividing it is a candidate. None for a ref that is about no branch.
func BranchesOf(ref string) []string {
	if branch, found := strings.CutPrefix(ref, "refs/heads/"); found {
		return []string{branch}
	}
	if branch, found := strings.CutPrefix(ref, "refs/g2g/forkpoints/"); found {
		return []string{branch}
	}
	rest, found := strings.CutPrefix(ref, "refs/g2g/synced/")
	if !found {
		return nil
	}
	branches := make([]string, 0, 1)
	for index, char := range rest {
		if char == '/' && index+1 < len(rest) {
			branches = append(branches, rest[index+1:])
		}
	}
	return branches
}

// Between is what changed from one reading to the next.
//
// touched says which branches the operation says it touches, and cuts the
// difference to them: the two readings are seconds apart in a repository
// other sessions are working in, and a branch one of them moved in that time
// is no part of this operation. Nil takes everything, for a command that
// cannot say.
func Between(before, after Reading, touched func(branch string) bool) Entry {
	concerns := func(branch string) bool { return touched == nil || touched(branch) }
	entry := Entry{}
	for _, ref := range union(before.Refs, after.Refs) {
		was, is := before.Refs[ref], after.Refs[ref]
		if was == is || !slices.ContainsFunc(BranchesOf(ref), concerns) {
			continue
		}
		if entry.Refs == nil {
			entry.Refs = map[string]Move{}
		}
		entry.Refs[ref] = Move{Before: was, After: is}
	}
	for _, branch := range union(before.Graph.Edges, after.Graph.Edges) {
		was, tracked := before.Graph.Edges[branch]
		is, still := after.Graph.Edges[branch]
		if (tracked == still && was == is) || !concerns(branch) {
			continue
		}
		if entry.Edges == nil {
			entry.Edges = map[string]Recorded{}
		}
		entry.Edges[branch] = Recorded{Before: present(was, tracked), After: present(is, still)}
		if tip, exists := after.Refs["refs/heads/"+branch]; exists {
			if entry.Tips == nil {
				entry.Tips = map[string]string{}
			}
			entry.Tips[branch] = tip
		}
	}
	if !slices.Equal(before.Graph.Trunks, after.Graph.Trunks) || !maps.Equal(before.Graph.Declared, after.Graph.Declared) {
		entry.RootsBefore, entry.RootsAfter = rootsOf(before.Graph), rootsOf(after.Graph)
	}
	if before.Here != after.Here {
		entry.Here = &Move{Before: before.Here, After: after.Here}
	}
	return entry
}

func present(edge graph.Edge, recorded bool) *graph.Edge {
	if !recorded {
		return nil
	}
	return &edge
}

func rootsOf(recorded graph.Graph) *Roots {
	return &Roots{Trunks: slices.Clone(recorded.Trunks), Declared: maps.Clone(recorded.Declared)}
}

func union[V any](one, other map[string]V) []string {
	names := slices.Collect(maps.Keys(one))
	for name := range other {
		if _, both := one[name]; !both {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// format is the layout of the file, and kept how many operations are held:
// enough to look back over a day's work, and few enough that the commits a
// deleted branch needs are still there.
const (
	format = 1
	kept   = 50
	// expiry is how long an entry is offered. Git keeps unreachable commits
	// for weeks by default, and an entry older than that may name ones that
	// are gone.
	expiry = 14 * 24 * time.Hour
)

// writes serialises this process's writes of the log. Two processes writing
// at once can still lose one of them an entry. A lost operation cannot then
// be undone, and what it changed still refuses the undo of anything older.
var writes sync.Mutex

// Log is the operations recorded in a repository, oldest first.
type Log struct {
	Format  int     `json:"format"`
	Entries []Entry `json:"entries"`
}

// Path is where the log is kept, under the Git common directory, so every
// worktree of a repository shares it.
func Path(common string) string { return filepath.Join(common, "g2g", "operations.json") }

// Load reads the log. One that is missing, unreadable or in another layout
// holds nothing: there is then nothing to undo, which is true.
func Load(path string) Log {
	content, err := os.ReadFile(path)
	if err != nil {
		return Log{Format: format}
	}
	var log Log
	if json.Unmarshal(content, &log) != nil || log.Format != format {
		return Log{Format: format}
	}
	return log
}

// Append adds an entry, dropping what is too old or too far back to offer.
func Append(path string, entry Entry, now time.Time) error {
	writes.Lock()
	defer writes.Unlock()
	log := Load(path)
	log.Entries = append(slices.DeleteFunc(log.Entries, func(old Entry) bool { return now.Sub(old.At) > expiry }), entry)
	if len(log.Entries) > kept {
		log.Entries = log.Entries[len(log.Entries)-kept:]
	}
	return save(path, log)
}

// MarkUndone records that the entry made at a moment has been put back.
func MarkUndone(path string, at time.Time) error {
	writes.Lock()
	defer writes.Unlock()
	log := Load(path)
	for index := range log.Entries {
		if log.Entries[index].At.Equal(at) {
			log.Entries[index].Undone = true
		}
	}
	return save(path, log)
}

func save(path string, log Log) error {
	log.Format = format
	content, err := json.MarshalIndent(log, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".operations-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if _, err := temporary.Write(append(content, '\n')); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), path)
}

// Latest is the newest operation not yet undone, which is the one an undo
// offers.
//
// published is the branches sent to a remote since that operation, by the
// operations recorded after it.
func (l Log) Latest(now time.Time) (entry Entry, published []string, found bool) {
	for _, each := range slices.Backward(l.Entries) {
		if now.Sub(each.At) > expiry {
			// Too old to offer, and so is everything before it.
			break
		}
		if len(each.Published) != 0 {
			published = append(published, each.Published...)
			continue
		}
		if !each.Undone {
			return each, published, true
		}
	}
	return Entry{}, nil, false
}

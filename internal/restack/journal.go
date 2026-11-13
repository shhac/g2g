package restack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/worktree"
)

// JournalSchemaVersion is bumped when a recorded field changes meaning. It is
// its own namespace, separate from the graph store's and from the CLI's
// output schema.
const JournalSchemaVersion = 2

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
	// Above is a durable replay boundary; version 1 readers must refuse it.
	Above         string `json:"above,omitempty"`
	SchemaVersion int    `json:"schemaVersion"`
	// Worktree owns Git's private rebase state, and the journal: each
	// worktree keeps its own, so one stopped on a conflict holds only what it
	// is moving.
	Worktree string `json:"worktree,omitempty"`
	// Checkout records the index's baseline before a bare ref move. A process
	// may die after moving the ref but before bringing the index along.
	CheckoutBranch string `json:"checkoutBranch,omitempty"`
	CheckoutTip    string `json:"checkoutTip,omitempty"`
	// OntoParent is the branch an explicit --onto asked the graph to record,
	// empty when the structure already says
	// where each branch belongs.
	OntoParent string `json:"onto,omitempty"`
	// OntoObject is the commit that --onto replays onto, when it is not the
	// branch it records: a remote's copy of it, and OntoNamed what it was
	// called. Empty replays onto OntoParent.
	OntoObject string `json:"ontoObject,omitempty"`
	OntoNamed  string `json:"ontoNamed,omitempty"`
	// Absorb records whether the user chose to keep commits their parent
	// dropped, so continuing does not silently change that decision.
	Absorb bool `json:"absorb,omitempty"`
	// Worktrees is what the restack was asked to do about a branch another
	// worktree holds, so continuing leaves the same branches where they are.
	// A journal written before it was recorded refuses, as restack then did.
	Worktrees worktree.Policy `json:"worktrees,omitempty"`
	// Followed is each worktree this restack brings along with its branch,
	// and the commit that worktree's index describes. It is written before
	// the branch moves and after each worktree is brought, so a restack that
	// dies between the two, or is aborted, can still put the files right.
	Followed map[string]FollowedTree `json:"followed,omitempty"`
	// Branch and Scope are the selection, so continuing covers the same set.
	Branch string `json:"branch,omitempty"`
	Scope  string `json:"scope,omitempty"`
	// ReturnTo is the branch the user was on, switched back to when the work
	// finishes or is aborted. Empty when they were on no branch.
	ReturnTo string `json:"returnTo,omitempty"`
	// Original maps every branch in the operation to its tip when the
	// operation began, after anything the caller moved first: pull's taken
	// branches stay taken. This is the whole reason for --abort.
	Original map[string]string `json:"original"`
	// Reparent maps a branch to the parent this operation moves it to. It is
	// recorded rather than re-derived because once the rewrite has happened
	// the branch no longer sits where the graph says, so a fresh plan reports
	// it as moved off its parent and can no longer tell where it was headed.
	Reparent map[string]string `json:"reparent,omitempty"`
	// Structure is every selected branch's recorded edge when the operation
	// began, read from the store then, so a fork pull recorded for a branch it
	// took is the one put back. A resumed pass writes fork points and reparenting as it goes --
	// planning against the old ones would see drift that is only its own
	// progress -- so --abort has to put those back as well as the tips, or it
	// leaves every branch recorded as forking where it no longer does.
	Structure map[string]RecordedEdge `json:"structure,omitempty"`
}

// FollowedTree is another worktree a restack brings along, and the commit its
// index and files describe.
type FollowedTree struct {
	Path string `json:"path"`
	Tip  string `json:"tip"`
}

// RecordedEdge is the part of a branch's edge a restack rewrites.
type RecordedEdge struct {
	Parent    string `json:"parent"`
	ForkPoint string `json:"forkPoint,omitempty"`
}

// Selection rebuilds the graph selection this record was started with.
func (r Record) Selection() graph.Selection {
	return graph.Selection{Branch: r.Branch, Scope: graph.Scope(r.Scope)}
}

// moving is every branch the restack may move, whether or not it has yet.
func (r Record) moving() map[string]bool {
	moving := map[string]bool{}
	add := func(branch string) {
		if branch != "" {
			moving[branch] = true
		}
	}
	for branch := range r.Original {
		add(branch)
	}
	for branch := range r.Reparent {
		add(branch)
	}
	for branch := range r.Structure {
		add(branch)
	}
	return moving
}

// footprint is what this restack moves and reads, for telling whether
// another may run beside it: every branch it may move, and those with the
// parents they are measured against and the branch an --onto names.
func (r Record) footprint() footprint {
	moves := r.moving()
	reads := maps.Clone(moves)
	for _, edge := range r.Structure {
		reads[edge.Parent] = true
	}
	for _, parent := range r.Reparent {
		reads[parent] = true
	}
	reads[r.OntoParent] = true
	delete(reads, "")
	return footprint{moves: moves, reads: reads}
}

// Journal stores the record for this worktree's in-flight restack.
type Journal interface {
	Load(context.Context) (Record, bool, error)
	Save(context.Context, Record) error
	Clear(context.Context) error
}

// Elsewhere is an unfinished restack another worktree owns, and the file
// that records it.
type Elsewhere struct {
	Record
	Path string
}

// othersJournal is a journal that can see the other worktrees' restacks. One
// that cannot has no others, which is a journal shared by every worktree.
type othersJournal interface {
	Others(context.Context) ([]Elsewhere, error)
}

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
type FileJournal struct {
	Git graph.Locator
}

// paths is where this worktree's journal is, where every journal is, and the
// file shared by every worktree before each had its own.
type journalPaths struct {
	own, legacy, linked string
	worktree            string
}

func (j FileJournal) paths(ctx context.Context) (journalPaths, error) {
	if j.Git == nil {
		return journalPaths{}, fmt.Errorf("restack journal is not configured")
	}
	common, err := j.Git.CommonDir(ctx)
	if err != nil {
		return journalPaths{}, err
	}
	paths := journalPaths{legacy: filepath.Join(common, "g2g", journalFileName), linked: filepath.Join(common, "g2g", "restack", "worktrees")}
	paths.own = paths.legacy
	locator, ok := j.Git.(WorktreeLocator)
	if !ok {
		return paths, nil
	}
	if paths.worktree, err = locator.WorktreeDir(ctx); err != nil {
		return journalPaths{}, err
	}
	if filepath.Clean(paths.worktree) != filepath.Clean(common) {
		paths.own = filepath.Join(paths.linked, filepath.Base(paths.worktree)+".json")
	}
	return paths, nil
}

// path is this worktree's journal. A linked worktree whose restack was
// journaled before each worktree had its own finds it in the shared file,
// which names it, and keeps using that file until the restack is finished.
func (j FileJournal) path(ctx context.Context) (string, error) {
	paths, err := j.paths(ctx)
	if err != nil || paths.own == paths.legacy {
		return paths.own, err
	}
	if _, err := os.Stat(paths.own); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return paths.own, nil
	}
	if legacy, found, err := readJournal(paths.legacy); err == nil && found && legacy.Worktree == paths.worktree {
		return paths.legacy, nil
	}
	return paths.own, nil
}

// Load returns this worktree's in-flight record. Absent is the ordinary
// state and not an error. The shared file, in the main worktree, may hold a
// record an older g2g wrote for a linked one; that is the linked one's.
func (j FileJournal) Load(ctx context.Context) (Record, bool, error) {
	paths, err := j.paths(ctx)
	if err != nil {
		return Record{}, false, err
	}
	path, err := j.path(ctx)
	if err != nil {
		return Record{}, false, err
	}
	record, found, err := readJournal(path)
	if err != nil || !found {
		return Record{}, false, err
	}
	if path == paths.legacy && record.Worktree != "" && paths.worktree != "" && record.Worktree != paths.worktree {
		return Record{}, false, nil
	}
	return record, true, nil
}

// Others is every unfinished restack another worktree owns, whether or not
// that worktree still exists: a record outlives its checkout, because the
// refs it moved are shared.
func (j FileJournal) Others(ctx context.Context) ([]Elsewhere, error) {
	paths, err := j.paths(ctx)
	if err != nil {
		return nil, err
	}
	own, err := j.path(ctx)
	if err != nil {
		return nil, err
	}
	candidates := []string{paths.legacy}
	linked, err := filepath.Glob(filepath.Join(paths.linked, "*.json"))
	if err != nil {
		return nil, err
	}
	candidates = append(candidates, linked...)
	others := make([]Elsewhere, 0)
	for _, path := range candidates {
		record, found, err := readJournal(path)
		if err != nil {
			return nil, err
		}
		// This worktree's own file is another's only when it names another:
		// the shared file an older g2g wrote a linked worktree's restack to.
		mine := path == own && (record.Worktree == "" || paths.worktree == "" || record.Worktree == paths.worktree)
		if found && !mine {
			others = append(others, Elsewhere{Record: record, Path: path})
		}
	}
	return others, nil
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
// half-parsed journal that blocks every later command.
func (j FileJournal) Save(ctx context.Context, record Record) error {
	return j.write(ctx, record, false)
}

// Start publishes a new journal only if none exists. Linking a complete
// temporary file is atomic and exclusive, unlike check-then-rename; two
// worktrees starting at once cannot replace each other's recovery state.
func (j FileJournal) Start(ctx context.Context, record Record) error {
	return j.write(ctx, record, true)
}

func (j FileJournal) write(ctx context.Context, record Record, exclusive bool) error {
	path, err := j.path(ctx)
	if err != nil {
		return err
	}
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

// Clear removes the record. Clearing when nothing is in flight is harmless,
// because finishing an operation that was never journalled is ordinary.
func (j FileJournal) Clear(ctx context.Context) error {
	path, err := j.path(ctx)
	if err != nil {
		return err
	}
	// The shared file may hold another worktree's restack, written before
	// each had its own; that is not this worktree's to forget.
	if _, mine, err := j.Load(ctx); err == nil && !mine {
		if _, found, _ := readJournal(path); found {
			return nil
		}
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove restack journal: %w", err)
	}
	return nil
}

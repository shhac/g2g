package restack

import (
	"context"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/worktree"
)

// JournalSchemaVersion is bumped when a recorded field changes meaning. It is
// its own namespace, separate from the graph store's and from the CLI's
// output schema.
const JournalSchemaVersion = 2

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
	// Worktree is the private Git directory whose rebase state this restack
	// owns. Which worktree's record it is goes by the file it is in (see
	// FileJournal); this names the owner of the one file every worktree
	// shared before each had its own.
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

// Journal stores each worktree's record of its in-flight restack. Load,
// Start, Save and Clear act on the record of the worktree asking; Unfinished
// is every worktree's, for the guards that decide what may run beside them.
type Journal interface {
	Load(context.Context) (Record, bool, error)
	Start(context.Context, Record) error
	Save(context.Context, Record) error
	Clear(context.Context) error
	Unfinished(context.Context) (*Record, []Elsewhere, error)
}

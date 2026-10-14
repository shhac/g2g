package syncpoint

import (
	"slices"
	"strings"

	localgit "github.com/shhac/g2g/internal/git"
)

// Drop is one commit a pull or push names one by one, what it does to it, and
// the branch it is about. To is where it went: the branch a moved commit is in
// now, or the commit a replaced one became.
type Drop struct {
	Kind   Kind
	Branch string
	Commit string
	To     string
}

// Kind is what a command does to a commit it names. The words are the ones
// machine output carries.
type Kind string

const (
	// Dropped leaves the branch and the stack.
	Dropped Kind = "dropped"
	// Moved leaves one branch for another the command also holds.
	Moved Kind = "moved"
	// Kept would have been dropped, and was kept as asked.
	Kept Kind = "kept"
	// Left was dropped here and is still on the remote: pull leaves it for
	// push to publish.
	Left Kind = "left"
	// Restored is put back by pull, missing only because the branch was
	// reset to a remote-tracking ref older than its sync point.
	Restored Kind = "restored"
	// Restoring is what push would put back after the remote dropped it,
	// which refuses the push.
	Restoring Kind = "restoring"
	// Replaced leaves the branch for the version of it a replay made, which
	// To names: one whose conflict was resolved, so its patch changed and
	// its content matches nothing on the other side.
	Replaced Kind = "replaced"
)

// kindOrder is the order a plan lists its commits in: each kind together,
// in the order the preview's notes say them.
var kindOrder = []Kind{Dropped, Moved, Replaced, Kept, Left, Restored, Restoring}

// Named lists commits of one kind on one branch.
func Named(kind Kind, branch string, commits []string) []Drop {
	named := make([]Drop, 0, len(commits))
	for _, commit := range commits {
		named = append(named, Drop{Kind: kind, Branch: branch, Commit: commit})
	}
	return named
}

// Of is the commits of one kind, in order.
func Of(commits []Drop, kind Kind) []Drop {
	of := make([]Drop, 0)
	for _, drop := range commits {
		if drop.Kind == kind {
			of = append(of, drop)
		}
	}
	return of
}

// Grouped is the commits each kind together, in kindOrder, each kind's in the
// order it was found -- which, for commits found branch by branch, is the
// selection's order.
func Grouped(commits []Drop) []Drop {
	grouped := slices.Clone(commits)
	slices.SortStableFunc(grouped, func(a, b Drop) int {
		return slices.Index(kindOrder, a.Kind) - slices.Index(kindOrder, b.Kind)
	})
	return grouped
}

// DroppedOn is the commits of the given kinds a plan names on one branch,
// which is what the agreement recorded for that branch carries. Which kinds
// count is the command's: a push records what it moved as well as what it
// dropped, because both left the branch on the remote; a pull only what it
// dropped.
func DroppedOn(branch string, commits []Drop, kinds ...Kind) []string {
	named := make([]string, 0)
	for _, drop := range commits {
		if drop.Branch == branch && slices.Contains(kinds, drop.Kind) {
			named = append(named, drop.Commit)
		}
	}
	return named
}

// Names is each commit by its branch and short id, as a preview lists them
// and a --keep asks for them back.
func Names(drops []Drop) string {
	named := make([]string, 0, len(drops))
	for _, drop := range drops {
		named = append(named, drop.Branch+" "+localgit.Short(drop.Commit))
	}
	return strings.Join(named, ", ")
}

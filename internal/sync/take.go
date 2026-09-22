package sync

import (
	"fmt"
	"slices"
	"strings"
)

// Which side wins where sync would otherwise refuse, and how far up the stack
// that answer applies.
//
// Parsing and error prose, no Git — the one part of this package that answers
// a question about a flag rather than about a repository.

// Side is which version of a diverged branch is kept.
//
// An enum rather than a boolean because the question has more answers than the
// one implemented, and naming the value leaves room for them. There is
// deliberately no "mine": sync only ever moves toward this checkout and push
// only ever moves toward the remote, so which side wins is normally answered by
// which command you run. This exists for the case neither command can reach.
type Side string

const (
	// SideNothing is the default: a divergence that would cost commits is
	// reported rather than resolved.
	SideNothing Side = ""
	// SidePublished discards local commits the published version does not have,
	// on the branches where the two have genuinely diverged.
	SidePublished Side = "published"
)

// Sides are the values a caller may name.
var Sides = []Side{SidePublished}

// Take is that choice together with the branch it applies through.
//
// One value rather than two arguments because it is one decision, and because
// the two are both strings: passed separately they are a transposition away
// from resolving a divergence against the wrong branch.
type Take struct {
	Side Side
	// Through is the last branch the side applies to, and empty means the whole
	// selection.
	//
	// A path from the trunk rather than an arbitrary set, because that is the
	// shape the question actually has: a branch's published version is built
	// on its parent's published version, so taking one and not the other
	// describes a stack that never existed. Divergence in a stack comes from
	// the bottom of it being rebased somewhere else, and the set that follows
	// from that is always the branch and what it is stacked on.
	Through string
}

// TakeNothing is the zero choice: refuse a divergence rather than resolve it.
var TakeNothing = Take{}

// Published reports the side that discards local commits.
func (t Take) Published() bool { return t.Side == SidePublished }

// Bounded reports a choice that stops part way up the stack.
func (t Take) Bounded() bool { return t.Through != "" }

// AppliesTo reports whether the chosen side applies to this branch.
//
// "Through" is ancestry, not position: the named branch and everything it is
// stacked on take the named side, and everything else keeps the default, which
// is to refuse rather than to pick silently. That refusal is the point — a
// boundary says where you have decided, not that you have decided everywhere.
//
// It was a position in the selection, which is a flattened tree. Where the
// stack forks, a sibling drawn before the boundary sat "below" it and was
// taken too, discarding work on a branch nobody had named.
//
// parents maps each branch to the one it is recorded on.
func (t Take) AppliesTo(branch string, parents map[string]string) bool {
	if !t.Published() {
		return false
	}
	if !t.Bounded() {
		return true
	}
	return stackedOn(t.Through, branch, parents)
}

// stackedOn reports whether ancestor is branch itself or somewhere below it.
//
// Bounded by the number of recorded edges, because a record naming a cycle
// must end the walk rather than the command.
func stackedOn(branch, ancestor string, parents map[string]string) bool {
	at := branch
	for range len(parents) + 1 {
		if at == ancestor {
			return true
		}
		parent, recorded := parents[at]
		if !recorded {
			return false
		}
		at = parent
	}
	return false
}

// ParseTake validates the pair.
//
// Through is refused without a side, because a boundary on a decision nobody
// made resolves nothing and would silently do the ordinary thing.
func ParseTake(value, through string) (Take, error) {
	take := Take{Through: through}
	if value != "" {
		if !slices.Contains(Sides, Side(value)) {
			names := make([]string, 0, len(Sides))
			for _, side := range Sides {
				names = append(names, string(side))
			}
			return TakeNothing, fmt.Errorf("unsupported --take %q (want %s)", value, strings.Join(names, ", "))
		}
		take.Side = Side(value)
	}
	if take.Bounded() && !take.Published() {
		return TakeNothing, fmt.Errorf("--through %q names where to stop taking a side, but no side was chosen · add --take %s", through, SidePublished)
	}
	return take, nil
}

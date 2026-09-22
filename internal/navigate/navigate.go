// Package navigate moves the checkout around a stack: up to the branch above,
// down to the one below, to the top, to the bottom.
//
// It is the one kind of command here that acts without a preview. Moving the
// checkout changes no ref, no record and no remote — git switch already
// refuses to carry a change it would clobber — so a preview step in front of
// it would only be in the way. What it keeps from every other command is the
// refusal to choose: at a fork it names the branches above and stops.
package navigate

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/stack"
)

// Direction is which way a move goes.
type Direction string

const (
	Up     Direction = "up"
	Down   Direction = "down"
	Top    Direction = "top"
	Bottom Direction = "bottom"
)

// Git is what a move needs from the repository.
type Git interface {
	CurrentBranch(ctx context.Context) (string, error)
	SwitchExisting(ctx context.Context, branch string) error
}

// Recorded answers what the g2g graph records directly on a branch.
//
// It exists for one case. A trunk is a branch nothing sits under, so no source
// describes it as a branch in a stack — and standing on one is exactly where
// someone types "up". The g2g graph does record what hangs from its own
// trunks, so that is asked, and only once resolution has found nothing.
type Recorded interface {
	Children(ctx context.Context, branch string) ([]string, error)
}

type Service struct {
	Selector stack.PathSelector
	Git      Git
	// Trunks is optional. Without it, a trunk nothing else describes is
	// refused the way any undescribed branch is.
	Trunks Recorded
}

// Ready reports a service with everything it needs, and is the rule the
// commands' registration uses.
func (s Service) Ready() bool { return s.Selector != nil && s.Git != nil }

// Request is one move: a direction, how many steps for up and down, and which
// record answers.
type Request struct {
	Direction Direction
	Steps     int
	// From and Trunk are the stack selection's own flags. The branch is always
	// the one the checkout is on, because a move is relative to where you are.
	From  stack.Source
	Trunk string
}

// Move is where a request leads, or why it cannot.
type Move struct {
	Direction Direction
	Steps     int
	Origin    string
	// Destination is where the checkout goes. It equals Origin when there is
	// nowhere to go and that is the answer — top from the top.
	Destination string
	// Base is the trunk the stack hangs from, and Walked is every branch the
	// move passed through, origin first and destination last.
	Base    string
	Walked  []string
	Parents map[string]string
	Source  stack.Source
	// Blocked is why the move cannot be made, and Repair is the same refusal as
	// structure. Blocked is always Repair's sentence.
	Blocked string
	Repair  repair.Note
}

// Arrived reports a move whose answer is where the checkout already is.
func (m Move) Arrived() bool { return m.Blocked == "" && m.Destination == m.Origin }

func (m Move) refuse(note repair.Note) Move {
	m.Repair = note
	m.Blocked = note.Sentence()
	return m
}

// Plan works out where a move leads without moving anything.
//
// An error is a failure to find out: no branch to move from, or a source that
// could not answer. A refusal — a fork, the end of the stack — is a Move with
// Blocked set, because it is an answer, and it carries the way out.
func (s Service) Plan(ctx context.Context, request Request) (Move, error) {
	if !s.Ready() {
		return Move{}, fmt.Errorf("navigation is not fully configured")
	}
	if request.Steps < 1 {
		request.Steps = 1
	}
	origin, err := s.Git.CurrentBranch(ctx)
	if err != nil {
		return Move{}, fmt.Errorf("HEAD is detached, so there is no branch to move from · git switch to a branch first")
	}
	move := Move{Direction: request.Direction, Steps: request.Steps, Origin: origin, Walked: []string{origin}}
	snapshot, err := s.selectFrom(ctx, request, origin)
	var undescribed stack.Undescribed
	if errors.As(err, &undescribed) {
		return s.fromTrunk(ctx, request, move, undescribed)
	}
	if err != nil {
		return Move{}, err
	}
	return walk(move, outlineOf(snapshot)), nil
}

func (s Service) selectFrom(ctx context.Context, request Request, branch string) (stack.Snapshot, error) {
	return s.Selector.Select(ctx, stack.Selection{
		Branch: branch,
		Trunk:  request.Trunk,
		Scope:  stack.ScopeStack,
		From:   request.From,
	}, "g2g "+string(request.Direction))
}

// fromTrunk answers from a branch no source describes, which is what a trunk
// of the g2g graph looks like to resolution.
//
// Only the step off the trunk is taken from the graph's own record. Everything
// after it is resolved from the branch it lands on, exactly as if the move had
// started there, so the rest of the walk goes through the same sources as any
// other.
func (s Service) fromTrunk(ctx context.Context, request Request, move Move, undescribed stack.Undescribed) (Move, error) {
	// The repository's default branch is a trunk whether or not anything is
	// stacked on it, and "nothing is stacked here" is the wrong answer to a
	// question about what is below it.
	if request.Direction == Down && undescribed.Trunk {
		move.Base = move.Origin
		return refuseBelowTrunk(move), nil
	}
	if s.Trunks == nil {
		return Move{}, undescribed
	}
	children, err := s.Trunks.Children(ctx, move.Origin)
	if err != nil {
		return Move{}, err
	}
	if len(children) == 0 {
		return Move{}, undescribed
	}
	move.Base = move.Origin
	move.Source = stack.SourceG2G
	if request.Direction == Down {
		return refuseBelowTrunk(move), nil
	}
	if len(children) > 1 {
		return refuseFork(move, move.Origin, children), nil
	}
	snapshot, err := s.selectFrom(ctx, request, children[0])
	if err != nil {
		return Move{}, err
	}
	return walk(move, outlineOf(snapshot)), nil
}

// outline is a stack reduced to what a walk needs.
type outline struct {
	base    string
	parents map[string]string
	members []string
	absent  []string
	source  stack.Source
}

// outlineOf reads a snapshot's edges. A selection that records none is a chain,
// which is what every source produced before selections could fork, so its
// order is its structure.
func outlineOf(snapshot stack.Snapshot) outline {
	parents := make(map[string]string, len(snapshot.Branches))
	previous := snapshot.Base
	for _, branch := range snapshot.Branches {
		parent, known := snapshot.ParentOf(branch)
		if !known && len(snapshot.Parents) == 0 {
			parent, known = previous, true
		}
		if known {
			parents[branch] = parent
		}
		previous = branch
	}
	return outline{
		base:    snapshot.Base,
		parents: parents,
		members: append([]string{snapshot.Base}, snapshot.Branches...),
		absent:  snapshot.Absent,
		source:  snapshot.Source,
	}
}

func (s outline) children(branch string) []string {
	children := make([]string, 0)
	for _, member := range s.members {
		if parent, ok := s.parents[member]; ok && parent == branch && member != branch {
			children = append(children, member)
		}
	}
	slices.Sort(children)
	return children
}

// walk takes the steps a request asks for from the last branch walked, and
// refuses at the first place the answer is not one branch.
func walk(move Move, within outline) Move {
	move.Base = within.base
	move.Parents = within.parents
	move.Source = within.source
	switch move.Direction {
	case Up:
		move = climb(move, within, move.Steps, false)
	case Top:
		move = climb(move, within, -1, true)
	case Down:
		move = descend(move, within)
	case Bottom:
		move = bottom(move, within)
	default:
		return move.refuse(repair.Note{Reason: fmt.Sprintf("unknown direction %q", move.Direction)})
	}
	if move.Blocked != "" {
		return move
	}
	if slices.Contains(within.absent, move.Destination) {
		return move.refuse(repair.Note{Reason: fmt.Sprintf("%s is not a local branch · the structure came from pull request bases, which describe branches this checkout does not have", move.Destination)})
	}
	return move
}

// climb follows single children. steps < 0 means to the end; toEnd says that
// running out of branches is arriving rather than falling short.
func climb(move Move, within outline, steps int, toEnd bool) Move {
	for taken := 0; steps < 0 || taken < steps; taken++ {
		current := move.Walked[len(move.Walked)-1]
		above := within.children(current)
		switch {
		case len(above) == 0 && toEnd:
			move.Destination = current
			return move
		case len(above) == 0:
			return refuseTop(move, current, taken)
		case len(above) > 1:
			return refuseFork(move, current, above)
		}
		move.Walked = append(move.Walked, above[0])
	}
	move.Destination = move.Walked[len(move.Walked)-1]
	return move
}

func descend(move Move, within outline) Move {
	for taken := 0; taken < move.Steps; taken++ {
		current := move.Walked[len(move.Walked)-1]
		if current == within.base {
			return refuseBelowTrunkAfter(move, taken)
		}
		parent, known := within.parents[current]
		if !known {
			return move.refuse(repair.Note{Reason: fmt.Sprintf("%s places nothing below %s", describeSource(within.source), current)})
		}
		move.Walked = append(move.Walked, parent)
	}
	move.Destination = move.Walked[len(move.Walked)-1]
	return move
}

// bottom is the first branch above the trunk on the path to where you stand.
// From the trunk itself that is the branch directly above it, which is only an
// answer when there is exactly one.
func bottom(move Move, within outline) Move {
	if move.Origin == within.base {
		return climb(move, within, 1, false)
	}
	path := []string{move.Origin}
	for current := move.Origin; ; {
		parent, known := within.parents[current]
		if !known {
			return move.refuse(repair.Note{Reason: fmt.Sprintf("%s places nothing below %s", describeSource(within.source), current)})
		}
		if parent == within.base {
			break
		}
		if slices.Contains(path, parent) {
			return move.refuse(repair.Note{Reason: fmt.Sprintf("%s is part of a parent cycle", current)})
		}
		path = append(path, parent)
		current = parent
	}
	move.Walked = path
	move.Destination = path[len(path)-1]
	return move
}

func refuseFork(move Move, at string, above []string) Move {
	ways := make([]repair.Step, 0, len(above))
	for _, child := range above {
		ways = append(ways, repair.Step{Command: "git switch " + child, Effect: "go up to " + child})
	}
	return move.refuse(repair.Note{
		Reason: fmt.Sprintf("%s has %d branches above it (%s), and choosing between them is a guess", at, len(above), strings.Join(above, ", ")),
		Ways:   ways,
	})
}

func refuseTop(move Move, at string, taken int) Move {
	if taken == 0 {
		return move.refuse(repair.Note{
			Reason: fmt.Sprintf("%s is the top of its stack, so nothing is above it", at),
			Ways:   []repair.Step{{Effect: "start a branch above it with g2g create"}},
		})
	}
	return move.refuse(repair.Note{
		Reason: fmt.Sprintf("%s is only %d below the top of its stack", move.Origin, taken),
		Ways:   []repair.Step{{Command: "g2g top", Effect: "go to the top"}},
	})
}

func refuseBelowTrunk(move Move) Move {
	return move.refuse(repair.Note{Reason: fmt.Sprintf("%s is the trunk, so nothing is below it", move.Origin)})
}

func refuseBelowTrunkAfter(move Move, taken int) Move {
	if taken == 0 {
		return refuseBelowTrunk(move)
	}
	return move.refuse(repair.Note{
		Reason: fmt.Sprintf("%s is only %d above the trunk", move.Origin, taken),
		Ways:   []repair.Step{{Command: fmt.Sprintf("g2g down %d", taken), Effect: "go to the trunk"}},
	})
}

// Switch moves the checkout to where the plan leads. It refuses a blocked plan
// and does nothing for one that has already arrived.
func (s Service) Switch(ctx context.Context, move Move) error {
	if move.Blocked != "" {
		return errors.New(move.Blocked)
	}
	if move.Arrived() {
		return nil
	}
	return s.Git.SwitchExisting(ctx, move.Destination)
}

func describeSource(source stack.Source) string {
	if source == "" {
		return "the structure"
	}
	return "the " + string(source) + " record"
}

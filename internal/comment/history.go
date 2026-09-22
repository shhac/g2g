package comment

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/shape"
)

// The merged history of a stack.
//
// Nothing local remembers a pull request that merged out of a stack: prune
// forgot the branch, land deleted it, and GitHub has no idea it was ever part
// of a stack. The comments do. Each records every pull request the stack has
// listed and the one each sat on, and a run reads that back.
//
// What a comment says is only believed when the comment is about this stack. A
// pull request moved in from another stack carries a comment describing that
// one, and adopting its history would list somebody else's merged work here —
// and then record it, so every later run would too. The test is the pull
// request's own recorded parent: still where it is, somewhere else in this
// stack, or merged into the branch it sits on now, which is exactly what
// happens to the branch below when it lands. Anything else is another stack.

// placement is where one of the stack's pull requests sits now.
type placement struct {
	// below is the pull request of the branch it sits on, zero when that
	// branch has none — the trunk, or a branch not yet published.
	below int
	// on is that branch.
	on string
}

// kept is one stack on the base: its branches, the pull requests they carry,
// and what their comments turn out to record.
type kept struct {
	branches []string
	own      map[int]placement
	// history maps each merged pull request to the one it sat on, as recorded.
	history map[int]int
	// unread are numbers the comments named that no round got to.
	unread []int
}

func newKept(forest shape.Forest, root string, members map[string]member) *kept {
	k := &kept{branches: forest.Subtree(root), own: map[int]placement{}, history: map[int]int{}}
	for _, branch := range k.branches {
		m := members[branch]
		if !m.listed() {
			continue
		}
		parent, _ := forest.Parent(branch)
		below := 0
		if members[parent].listed() {
			below = members[parent].number
		}
		k.own[m.number] = placement{below: below, on: parent}
	}
	return k
}

// readHistory reads every stack's conversations and follows what their
// comments record until nothing new is named.
//
// The stacks share one cache and one round trip per round, because one query
// answers any number of pull requests, and each walks its own history over
// it. A stack decides from the cache as a whole rather than from what it asked
// for itself, so a number two stacks both name is read once and seen by both
// — which is what makes a run from a trunk agree with a run from one of its
// stacks.
func (s Service) readHistory(ctx context.Context, stacks []*kept) (map[int]githubstack.Conversation, error) {
	read := map[int]githubstack.Conversation{}
	asked := map[int]bool{}
	for round := 0; round < historyRounds; round++ {
		wanted := make([]int, 0)
		for _, kept := range stacks {
			for _, number := range kept.walk(read, asked) {
				if !asked[number] {
					asked[number] = true
					wanted = append(wanted, number)
				}
			}
		}
		if len(wanted) == 0 {
			break
		}
		slices.Sort(wanted)
		conversations, err := s.GitHub.Conversations(ctx, wanted, Marker)
		if err != nil {
			return nil, err
		}
		for _, conversation := range conversations {
			read[conversation.Number] = conversation
		}
	}
	for _, kept := range stacks {
		for number := range kept.own {
			if _, answered := read[number]; !answered {
				return nil, fmt.Errorf("GitHub did not answer for pull request #%d, which this stack carries", number)
			}
		}
		kept.unread = kept.walk(read, asked)
		slices.Sort(kept.unread)
	}
	return read, nil
}

// walk decides the history from what has been read, and returns the numbers
// it would need read to decide more. A number asked for and not answered is
// an issue or nothing at all, and is not waited on.
func (k *kept) walk(read map[int]githubstack.Conversation, asked map[int]bool) []int {
	need := map[int]bool{}
	wait := func(number int) {
		if !asked[number] {
			need[number] = true
		}
	}
	k.history = map[int]int{}
	sources := make([]int, 0, len(k.own))
	for _, number := range slices.Sorted(maps.Keys(k.own)) {
		conversation, ok := read[number]
		if !ok {
			wait(number)
			continue
		}
		trusted, pending := k.trusts(number, conversation, read)
		if pending != 0 {
			wait(pending)
			continue
		}
		if trusted {
			sources = append(sources, number)
		}
	}
	seen := map[int]bool{}
	for index := 0; index < len(sources); index++ {
		for _, found := range read[sources[index]].Comments {
			for _, recorded := range recordedIn(found.Body) {
				if _, own := k.own[recorded.Number]; own || seen[recorded.Number] {
					continue
				}
				conversation, ok := read[recorded.Number]
				if !ok {
					wait(recorded.Number)
					continue
				}
				seen[recorded.Number] = true
				if conversation.Merged() {
					k.history[recorded.Number] = recorded.Parent
					sources = append(sources, recorded.Number)
				}
			}
		}
	}
	return slices.Sorted(maps.Keys(need))
}

// trusts reports whether a pull request's comment describes this stack, and
// names a pull request that has to be read before that can be said.
func (k *kept) trusts(number int, conversation githubstack.Conversation, read map[int]githubstack.Conversation) (bool, int) {
	here := k.own[number]
	for _, found := range conversation.Comments {
		for _, recorded := range recordedIn(found.Body) {
			if recorded.Number != number {
				continue
			}
			if _, within := k.own[recorded.Parent]; within || recorded.Parent == here.below || recorded.Parent == 0 {
				return true, 0
			}
			below, ok := read[recorded.Parent]
			if !ok {
				return false, recorded.Parent
			}
			return below.Merged() && below.Base == here.on, 0
		}
	}
	return false, 0
}

// merged is the history, oldest pull request first.
func (k *kept) merged() []int {
	return slices.Sorted(maps.Keys(k.history))
}

// recorded is what every comment of this stack writes down for the next run:
// each pull request it carries with the one below it, then the history as it
// was recorded.
func (k *kept) recorded() []entry {
	entries := make([]entry, 0, len(k.own)+len(k.history))
	for _, number := range slices.Sorted(maps.Keys(k.own)) {
		entries = append(entries, entry{Number: number, Parent: k.own[number].below})
	}
	for _, number := range k.merged() {
		entries = append(entries, entry{Number: number, Parent: k.history[number]})
	}
	slices.SortFunc(entries, func(left, right entry) int { return left.Number - right.Number })
	return entries
}

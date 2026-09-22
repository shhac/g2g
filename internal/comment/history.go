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
	// found is what the history walk settled on. It is set once, after the
	// last round, and everything that renders the stack reads it.
	found walked
}

// walked is one pass of the history walk over what has been read.
type walked struct {
	// history maps each merged pull request to the one it sat on, as recorded.
	history map[int]int
	// recorded is every entry a trusted comment wrote down, first mention
	// first, which is what says whether a merged pull request had children
	// in another stack.
	recorded []entry
	// need are numbers the pass would have to read to decide more.
	need []int
}

func newKept(forest shape.Forest, root string, members map[string]Member) *kept {
	k := &kept{branches: forest.Subtree(root), own: map[int]placement{}}
	for _, branch := range k.branches {
		number := members[branch].listedNumber()
		if number == 0 {
			continue
		}
		parent, _ := forest.Parent(branch)
		k.own[number] = placement{below: members[parent].listedNumber(), on: parent}
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
			for _, number := range kept.walk(read, asked).need {
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
		kept.found = kept.walk(read, asked)
	}
	return read, nil
}

// walk decides the history from what has been read, changing nothing. A
// number asked for and not answered is an issue or nothing at all, and is not
// waited on.
func (k *kept) walk(read map[int]githubstack.Conversation, asked map[int]bool) walked {
	need := map[int]bool{}
	wait := func(number int) {
		if !asked[number] {
			need[number] = true
		}
	}
	sources := k.trustedSources(read, wait)
	found := k.follow(sources, read, wait)
	found.need = slices.Sorted(maps.Keys(need))
	return found
}

// trustedSources are the stack's own pull requests whose comment describes
// this stack.
func (k *kept) trustedSources(read map[int]githubstack.Conversation, wait func(int)) []int {
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
	return sources
}

// follow reads what the trusted comments record, and keeps each recorded pull
// request that merged. A merged one's own comment is trusted in turn, since it
// was written about this stack.
func (k *kept) follow(sources []int, read map[int]githubstack.Conversation, wait func(int)) walked {
	found := walked{history: map[int]int{}}
	seen := map[int]bool{}
	for index := 0; index < len(sources); index++ {
		for _, recorded := range recordsOf(read[sources[index]]) {
			if seen[recorded.Number] {
				continue
			}
			found.recorded = append(found.recorded, recorded)
			if _, own := k.own[recorded.Number]; own {
				seen[recorded.Number] = true
				continue
			}
			conversation, ok := read[recorded.Number]
			if !ok {
				wait(recorded.Number)
				continue
			}
			seen[recorded.Number] = true
			if conversation.Merged() {
				found.history[recorded.Number] = recorded.Parent
				sources = append(sources, recorded.Number)
			}
		}
	}
	return found
}

// trusts reports whether a pull request's comment describes this stack, and
// names a pull request that has to be read before that can be said.
//
// The comment's own entry says what the pull request sat on when it was
// written. That still fits when it is in this stack, or the trunk, or a pull
// request that merged into the branch it sits on now — which is what happens
// to the branch above when the one below lands. Anything else is another
// stack's comment, brought along by a pull request that moved.
func (k *kept) trusts(number int, conversation githubstack.Conversation, read map[int]githubstack.Conversation) (bool, int) {
	parent, recorded := recordedParent(conversation, number)
	if !recorded {
		return false, 0
	}
	if _, within := k.own[parent]; within || parent == 0 {
		return true, 0
	}
	below, ok := read[parent]
	if !ok {
		return false, parent
	}
	return below.Merged() && below.Base == k.own[number].on, 0
}

// recordsOf is every entry a pull request's marked comments record, read only
// from comments the person running this can edit.
//
// Anyone who can comment can post a comment opening with the marker, and its
// entries would otherwise be believed — a made-up merged number would join the
// history and be recorded by every later run. A comment you can edit is one
// you, or someone with the same access to the pull request, wrote.
func recordsOf(conversation githubstack.Conversation) []entry {
	entries := make([]entry, 0)
	for _, found := range editable(conversation.Comments) {
		entries = append(entries, recordedIn(found.Body)...)
	}
	return entries
}

// editable is the marked comments the person running this can change.
func editable(comments []githubstack.Comment) []githubstack.Comment {
	kept := make([]githubstack.Comment, 0, len(comments))
	for _, found := range comments {
		if found.Editable {
			kept = append(kept, found)
		}
	}
	return kept
}

// recordedParent is what a pull request's own comment says it sat on.
func recordedParent(conversation githubstack.Conversation, number int) (int, bool) {
	for _, recorded := range recordsOf(conversation) {
		if recorded.Number == number {
			return recorded.Parent, true
		}
	}
	return 0, false
}

// merged is the history, oldest pull request first.
func (k *kept) merged() []int { return slices.Sorted(maps.Keys(k.found.history)) }

// unread are numbers the comments named that no round got to.
func (k *kept) unread() []int { return k.found.need }

// shared reports a merged pull request that also had children outside this
// stack: the branch a fork grew from, merged. Its comment cannot be drawn from
// one of the stacks it fed without undoing what a run from the other wrote.
func (k *kept) shared(number int) bool {
	for _, recorded := range k.found.recorded {
		if recorded.Parent != number {
			continue
		}
		_, own := k.own[recorded.Number]
		_, history := k.found.history[recorded.Number]
		if !own && !history {
			return true
		}
	}
	return false
}

// recorded is what every comment of this stack writes down for the next run:
// each pull request it carries with the one below it, the history as it was
// recorded, and the numbers this run did not reach, so the next one can.
func (k *kept) recorded() []entry {
	entries := make([]entry, 0, len(k.own)+len(k.found.history)+len(k.found.need))
	for number, place := range k.own {
		entries = append(entries, entry{Number: number, Parent: place.below})
	}
	for number, parent := range k.found.history {
		entries = append(entries, entry{Number: number, Parent: parent})
	}
	for _, number := range k.found.need {
		if _, own := k.own[number]; !own {
			entries = append(entries, entry{Number: number})
		}
	}
	slices.SortFunc(entries, func(left, right entry) int { return left.Number - right.Number })
	return slices.CompactFunc(entries, func(left, right entry) bool { return left.Number == right.Number })
}

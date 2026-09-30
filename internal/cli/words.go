// The words every view shares: counting, listing branches, and writing a
// command so it can be copied into a shell.
package cli

import (
	"fmt"
	"strings"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/repair"
)

func count(total int, singular, plural string) string {
	return fmt.Sprintf("%d %s", total, pick(total, singular, plural))
}

// pick chooses the form that agrees with a count. Two helpers were doing this,
// one counting and one not, which is one idea wearing two names.
func pick(total int, one, many string) string {
	if total == 1 {
		return one
	}
	return many
}

// branchList renders one or more branch names as a readable subject.
func branchList(branches []string) string {
	switch len(branches) {
	case 1:
		return branches[0]
	case 2:
		return branches[0] + " and " + branches[1]
	default:
		return strings.Join(branches[:len(branches)-1], ", ") + " and " + branches[len(branches)-1]
	}
}

func commandText(command []string) string {
	return repair.Command(command)
}

// shellQuote leaves an argument alone when every rune in it is safe, and quotes
// it otherwise.
//
// The condition used to be the negation of a character class, inverted again by
// IndexFunc, and compared against < 0 — three negations to say "all of these
// are safe", on the path that renders a command the reader is invited to paste.
func shellQuote(argument string) string {
	return repair.Command([]string{argument})
}

// landingPhrase is where a declared trunk goes, as the tail of a sentence.
func landingPhrase(declaration graph.Declaration) string {
	if !declaration.Lands() {
		return ""
	}
	return fmt.Sprintf(" that lands into %s by %s", declaration.Into, declaration.By)
}

// The words every view shares: counting, listing branches, and writing a
// command so it can be copied into a shell.
package cli

import (
	"fmt"
	"strings"
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
	parts := make([]string, len(command))
	for index, argument := range command {
		parts[index] = shellQuote(argument)
	}
	return strings.Join(parts, " ")
}

// shellQuote leaves an argument alone when every rune in it is safe, and quotes
// it otherwise.
//
// The condition used to be the negation of a character class, inverted again by
// IndexFunc, and compared against < 0 — three negations to say "all of these
// are safe", on the path that renders a command the reader is invited to paste.
func shellQuote(argument string) string {
	if argument != "" && !strings.ContainsFunc(argument, func(r rune) bool { return !shellSafe(r) }) {
		return argument
	}
	return "'" + strings.ReplaceAll(argument, "'", "'\\''") + "'"
}

// shellSafe is the set of runes a POSIX shell passes through untouched, stated
// positively so it can be read and tested as a list rather than inverted.
func shellSafe(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	default:
		return strings.ContainsRune("_+-./:=@", r)
	}
}

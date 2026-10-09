// Package words holds the rules every sentence g2g writes shares: which form
// of a word agrees with a count, and how a list of names reads. Five packages
// each carried their own pick, and two their own list, so it depends on
// nothing and any of them may use it.
package words

import (
	"strconv"
	"strings"
)

// Pick is the form that agrees with a count: one for exactly one, many for
// any other number, none included.
func Pick(count int, one, many string) string {
	if count == 1 {
		return one
	}
	return many
}

// Count is a count with the form that agrees with it: "1 commit", "3 commits".
func Count(count int, one, many string) string {
	return strconv.Itoa(count) + " " + Pick(count, one, many)
}

// List names items as the subject of a sentence: "a", "a and b",
// "a, b and c". An empty list is empty.
func List(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

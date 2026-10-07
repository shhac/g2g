package comment

import (
	"regexp"
	"strconv"
	"strings"
)

// recordedIn reads back what a comment recorded. Anything it cannot read is
// ignored rather than refused: the comment is editable by anyone who can edit
// the pull request, and a stray character is no reason to stop keeping the
// rest of it.
func recordedIn(body string) []entry {
	list, known := recordedList(body)
	if !known {
		return nil
	}
	return entriesIn(list)
}

// readable reports whether this g2g knows the format a comment's data line is
// in. One with no data line has nothing to lose by being rewritten.
func readable(body string) bool {
	_, known := recordedList(body)
	return known
}

// recordedList is the list a comment's data line records, and whether this
// g2g knows the format it is in. It is one rule for both readers, because a
// comment judged readable and then read as empty would be rewritten without
// the history it holds.
func recordedList(body string) (string, bool) {
	said, ok := dataIn(body)
	if !ok {
		return "", true
	}
	fields := fieldsOf(said)
	version, versioned := fields["v"]
	switch {
	case !versioned:
		// The bare list 0.43.0 and earlier wrote. Remove this case once their
		// comments have been rewritten: any still unread then loses its
		// history.
		return said, true
	case version == dataVersion:
		return fields["prs"], true
	default:
		return "", false
	}
}

// dataIn is what a comment's data line says between its opener and its close.
func dataIn(body string) (string, bool) {
	start := strings.Index(body, dataOpen)
	if start < 0 {
		return "", false
	}
	rest := body[start+len(dataOpen):]
	end := strings.Index(rest, dataClose)
	if end < 0 {
		return "", false
	}
	return rest[:end], true
}

func entriesIn(list string) []entry {
	entries := make([]entry, 0)
	seen := map[int]bool{}
	for _, field := range strings.Split(list, ",") {
		number, parent, ok := parseEntry(strings.TrimSpace(field))
		if !ok || seen[number] {
			continue
		}
		seen[number] = true
		entries = append(entries, entry{Number: number, Parent: parent})
		if len(entries) == recordedLimit {
			break
		}
	}
	return entries
}

func parseEntry(field string) (int, int, bool) {
	own, below, sits := strings.Cut(field, ">")
	number, err := strconv.Atoi(own)
	if err != nil || number <= 0 {
		return 0, 0, false
	}
	if !sits {
		return number, 0, true
	}
	parent, err := strconv.Atoi(below)
	if err != nil || parent <= 0 || parent == number {
		return 0, 0, false
	}
	return number, parent, true
}

// same reports whether an existing comment already says what this one would,
// by its rev rather than its text.
//
// Comparing the text made keeping the comments depend on GitHub handing back
// exactly what was sent. It stores a body edited in a browser with CRLF line
// endings, and had it ever normalised anything else -- whitespace, how a
// character is encoded -- every run would have found every comment changed and
// rewritten it. A rev is this tool's own record of what it wrote. A comment
// with none is from before revs, and is rewritten once.
func same(existing, rendered string) bool {
	had := revIn(existing)
	return had != "" && had == revIn(rendered)
}

// revIn reads the rev from a comment's first line, empty when it has none.
func revIn(body string) string {
	found := markerFields(body)["rev"]
	if !revShape.MatchString(found) {
		return ""
	}
	return found
}

var revShape = regexp.MustCompile(`^[0-9a-f]{16}$`)

// markerFields reads the `key=value` fields of a comment's first line by name,
// whichever g2g wrote them and in whatever order, nil when that line is not
// this tool's marker.
func markerFields(body string) map[string]string {
	first, _, _ := strings.Cut(strings.TrimSpace(body), "\n")
	rest, ok := strings.CutPrefix(strings.TrimSpace(first), Marker)
	if !ok {
		return nil
	}
	inner, ok := strings.CutSuffix(rest, "-->")
	if !ok || (inner != "" && !strings.HasPrefix(inner, " ")) {
		return nil
	}
	return fieldsOf(inner)
}

// fieldsOf reads `key=value` fields separated by spaces, skipping anything
// that is not one.
func fieldsOf(said string) map[string]string {
	fields := map[string]string{}
	for _, field := range strings.Fields(said) {
		if key, value, ok := strings.Cut(field, "="); ok {
			fields[key] = value
		}
	}
	return fields
}

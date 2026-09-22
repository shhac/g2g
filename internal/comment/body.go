package comment

import (
	"fmt"
	"strconv"
	"strings"
)

// Marker identifies the comment this command keeps. It is an HTML comment, so
// GitHub renders nothing for it, and it is the only thing that makes a comment
// this tool's to edit: a comment without it is somebody's words.
const Marker = "<!-- g2g:stack-comment -->"

// The data line records every pull request the stack has listed, each with the
// pull request it sat on: `11,12>11,13>12`. It is what lets a merged pull
// request go on being listed after its branch is pruned and deleted, when
// nothing local remembers it and GitHub has no idea it was ever part of a
// stack; the parent is what tells a comment about this stack from one a pull
// request brought with it from another.
//
// Only numbers go inside it, so nothing a branch name or a title contains can
// close the HTML comment and spill into what GitHub renders.
const (
	dataOpen  = "<!-- g2g:stack-prs "
	dataClose = " -->"
)

// recordedLimit bounds how many pull requests one comment may hand the next
// run. A person can edit a comment, and an edit is not a reason to read a
// thousand pull requests.
const recordedLimit = 200

// entry is one recorded pull request and the one it sat on, zero for none.
type entry struct {
	Number int
	Parent int
}

// line is one entry of the list a comment draws.
type line struct {
	Branch string
	Number int
	State  State
	Depth  int
	Trunk  bool
}

// view is everything one comment says: the stack as seen from one pull
// request, and the pull requests that merged out of it.
type view struct {
	Trunk  string
	Merged []int
	Lines  []line
	// Here is the pull request this comment is on.
	Here int
	// Recorded is every pull request the stack knows of, for the next run. It
	// is the same on every comment of a stack.
	Recorded []entry
}

func (v view) body() string {
	var out strings.Builder
	out.WriteString(Marker + "\n")
	out.WriteString("**Stack**\n\n")
	if len(v.Merged) != 0 {
		merged := make([]string, 0, len(v.Merged))
		for _, number := range v.Merged {
			merged = append(merged, v.reference(number))
		}
		fmt.Fprintf(&out, "Merged into %s: %s\n\n", code(v.Trunk), strings.Join(merged, " · "))
	}
	for _, entry := range v.Lines {
		out.WriteString(strings.Repeat("  ", entry.Depth) + "- " + v.item(entry) + "\n")
	}
	out.WriteString("\n<sub>Kept up to date by g2g, which edits this comment when the stack changes.</sub>\n")
	out.WriteString(dataOpen + encode(v.Recorded) + dataClose + "\n")
	return out.String()
}

// reference is a pull request number GitHub will link, emphasised where it is
// this one.
func (v view) reference(number int) string {
	if number == v.Here {
		return "**#" + strconv.Itoa(number) + "** 👈 this pull request"
	}
	return "#" + strconv.Itoa(number)
}

func (v view) item(entry line) string {
	if entry.Trunk {
		return code(entry.Branch)
	}
	if entry.Number == 0 && entry.State == StateMissing {
		return code(entry.Branch) + " · no pull request yet"
	}
	if entry.Number == 0 {
		return code(entry.Branch) + " · no open pull request"
	}
	said := "#" + strconv.Itoa(entry.Number) + " " + code(entry.Branch)
	if entry.State == StateMerged {
		said += " · merged"
	}
	if entry.Number == v.Here {
		return "**" + said + "** 👈 this pull request"
	}
	return said
}

// code writes text as a Markdown code span that nothing in it can close. Git
// allows a backtick in a branch name, and a span is ended by a run of backticks
// as long as the one that opened it.
func code(text string) string {
	longest, run := 0, 0
	for _, character := range text {
		if character != '`' {
			run = 0
			continue
		}
		run++
		longest = max(longest, run)
	}
	fence := strings.Repeat("`", longest+1)
	if longest == 0 {
		return fence + text + fence
	}
	return fence + " " + text + " " + fence
}

func encode(entries []entry) string {
	said := make([]string, 0, len(entries))
	for _, recorded := range entries {
		if recorded.Parent == 0 {
			said = append(said, strconv.Itoa(recorded.Number))
			continue
		}
		said = append(said, strconv.Itoa(recorded.Number)+">"+strconv.Itoa(recorded.Parent))
	}
	return strings.Join(said, ",")
}

// recordedIn reads back what a comment recorded. Anything it cannot read is
// ignored rather than refused: the comment is editable by anyone who can edit
// the pull request, and a stray character is no reason to stop keeping the
// rest of it.
func recordedIn(body string) []entry {
	start := strings.Index(body, dataOpen)
	if start < 0 {
		return nil
	}
	rest := body[start+len(dataOpen):]
	end := strings.Index(rest, dataClose)
	if end < 0 {
		return nil
	}
	entries := make([]entry, 0)
	seen := map[int]bool{}
	for _, field := range strings.Split(rest[:end], ",") {
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

// same reports whether an existing comment already says what this one would.
// GitHub stores a body edited in a browser with CRLF line endings, and that is
// not a reason to edit it again.
func same(existing, rendered string) bool {
	normalise := func(body string) string {
		return strings.TrimSpace(strings.ReplaceAll(body, "\r\n", "\n"))
	}
	return normalise(existing) == normalise(rendered)
}

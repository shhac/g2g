package comment

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Marker identifies the comment this command keeps. It is an HTML comment, so
// GitHub renders nothing for it, and it is the only thing that makes a comment
// this tool's to edit: a comment without it is somebody's words.
const Marker = "<!-- g2g:stack-comment -->"

// The data line records every pull request the stack has listed. It is what
// lets a merged pull request go on being listed after its branch is pruned and
// deleted, when nothing local remembers it and GitHub has no idea it was ever
// part of a stack.
//
// Only numbers go inside it, so nothing a branch name or a title contains can
// close the HTML comment and spill into what GitHub renders.
const (
	dataOpen  = "<!-- g2g:stack-prs "
	dataClose = " -->"
)

// listedLimit bounds how many numbers one comment may hand the next run. A
// person can edit a comment, and an edit is not a reason to read a thousand
// pull requests.
const listedLimit = 200

// line is one entry of the list a comment draws.
type line struct {
	Branch string
	Number int
	State  string
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
	// Listed is every pull request the stack knows of, recorded for the next
	// run. It is the same on every comment of a stack.
	Listed []int
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
	out.WriteString(dataOpen + joinNumbers(v.Listed) + dataClose + "\n")
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
	if entry.Trunk || entry.Number == 0 {
		said := code(entry.Branch)
		switch {
		case entry.Trunk:
		case entry.State == stateMissing:
			said += " · no pull request yet"
		default:
			said += " · no open pull request"
		}
		return said
	}
	said := "#" + strconv.Itoa(entry.Number) + " " + code(entry.Branch)
	if entry.State == stateMerged {
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

func joinNumbers(numbers []int) string {
	said := make([]string, 0, len(numbers))
	for _, number := range numbers {
		said = append(said, strconv.Itoa(number))
	}
	return strings.Join(said, ",")
}

// listedIn reads back the pull requests a comment recorded. Anything it cannot
// read as a positive number is ignored rather than refused: the comment is
// editable by anyone who can edit the pull request, and a stray character is
// no reason to stop keeping the rest of it.
func listedIn(body string) []int {
	start := strings.Index(body, dataOpen)
	if start < 0 {
		return nil
	}
	rest := body[start+len(dataOpen):]
	end := strings.Index(rest, dataClose)
	if end < 0 {
		return nil
	}
	numbers := make([]int, 0)
	for _, field := range strings.Split(rest[:end], ",") {
		number, err := strconv.Atoi(strings.TrimSpace(field))
		if err != nil || number <= 0 || slices.Contains(numbers, number) {
			continue
		}
		numbers = append(numbers, number)
		if len(numbers) == listedLimit {
			break
		}
	}
	return numbers
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

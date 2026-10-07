package comment

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Marker opens the comment this command keeps, and is how one is recognised.
// It begins an HTML comment, so GitHub renders nothing for it, and it is the
// only thing that makes a comment this tool's to edit: a comment without it is
// somebody's words. The line it opens goes on to carry the comment's fields,
// `rev=` and `version=`, which a reader takes by name and ignores the rest of:
// a g2g that knew only the fields it wrote would take a newer one's comment
// for one it could not read, and rewrite it.
const Marker = "<!-- g2g:stack-comment"

// The data line records every pull request the stack has listed, each with the
// pull request it sat on: `v=1 prs=11,12>11,13>12`. It is what lets a merged pull
// request go on being listed after its branch is pruned and deleted, when
// nothing local remembers it and GitHub has no idea it was ever part of a
// stack; the parent is what tells a comment about this stack from one a pull
// request brought with it from another.
//
// Only numbers go inside it, so nothing a branch name or a title contains can
// close the HTML comment and spill into what GitHub renders.
//
// Its fields are read by name, and `v` says which format the rest is in. A
// comment in a format this g2g does not know is left alone rather than
// rewritten, which would drop the history it could not read. A line with no
// `v` is from 0.43.0 or earlier: the bare list, which recordedList reads.
const (
	dataOpen    = "<!-- g2g:stack-prs "
	dataClose   = " -->"
	dataVersion = "1"
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
	// Above is how many pull requests sit on a branch drawn beside the path
	// rather than on it, which is all a comment says about them.
	Above int
}

// view is everything one comment says: the stack as seen from one pull
// request, with the pull requests that merged out of it in the places they
// sat.
type view struct {
	Lines []line
	// Here is the pull request this comment is on.
	Here int
	// Recorded is every pull request the stack knows of, for the next run. It
	// is the same on every comment of a stack.
	Recorded []entry
	// Version is the g2g writing it, named in the footer.
	Version string
}

func (v view) body() string {
	var stack strings.Builder
	stack.WriteString("**Stack**\n\n")
	for _, entry := range v.Lines {
		stack.WriteString(strings.Repeat("  ", entry.Depth) + "- " + v.item(entry) + "\n")
	}
	data := dataOpen + "v=" + dataVersion + " prs=" + encode(v.Recorded) + dataClose + "\n"
	footer := "\n<sub>This comment is managed by [g2g](" + homepage + ") and updates automatically when the stack changes" + v.lastUpdated() + "</sub>\n"
	return Marker + " rev=" + rev(stack.String()+data) + v.versionField() + " -->\n" + stack.String() + footer + data
}

// lastUpdated names the g2g that wrote the comment, which is not necessarily
// the one managing it now: the footer is left out of the rev, so a newer g2g
// leaves a comment alone until the stack changes. A release links to its own
// notes, and a build that is not one to the homepage.
func (v view) lastUpdated() string {
	if !versionShape.MatchString(v.Version) {
		return ""
	}
	link := homepage
	if releaseShape.MatchString(v.Version) {
		link = releases + v.Version
	}
	return " · last updated by [g2g@" + v.Version + "](" + link + ")"
}

// versionField names the g2g that last wrote a comment, for g2g as the footer
// does for a reader. It is left out of the rev with the footer, and out of the
// comment entirely for a version that could end the HTML comment it sits in or
// run into the field after it.
func (v view) versionField() string {
	if !versionShape.MatchString(v.Version) {
		return ""
	}
	return " version=" + v.Version
}

var (
	versionShape = regexp.MustCompile(`^[0-9A-Za-z.+_-]+$`)
	releaseShape = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
)

// rev names what a comment says: the stack it draws and what it records for
// the next run, and not the footer, so a newer g2g or a changed link is not a
// reason to edit it. It is only hexadecimal, so like the data line nothing a
// branch name contains can reach the HTML comment it sits in.
func rev(said string) string {
	sum := sha256.Sum256([]byte(said))
	return hex.EncodeToString(sum[:8])
}

func (v view) item(entry line) string {
	if entry.Above == 0 {
		return v.label(entry)
	}
	return fmt.Sprintf("%s · +%d above", v.label(entry), entry.Above)
}

func (v view) label(entry line) string {
	if entry.Trunk && entry.Number != 0 {
		return "base #" + strconv.Itoa(entry.Number) + " " + code(entry.Branch)
	}
	if entry.Trunk {
		return "base " + code(entry.Branch)
	}
	if entry.Number == 0 && entry.State == StateMissing {
		return code(entry.Branch) + " · no pull request yet"
	}
	if entry.Number == 0 {
		return code(entry.Branch) + " · no open pull request"
	}
	said := "#" + strconv.Itoa(entry.Number)
	// A merged pull request's branch is named by what GitHub still reports
	// for it, which can be nothing; an empty code span would render as two
	// stray backticks.
	if entry.Branch != "" {
		said += " " + code(entry.Branch)
	}
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

// homepage is where the footer's naming of g2g links to, and releases where a
// released version's does, by its tag.
const (
	homepage = "https://g2g.foo"
	releases = "https://github.com/shhac/g2g/releases/tag/v"
)

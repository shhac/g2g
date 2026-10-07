package cli

import (
	"slices"
	"strings"

	"github.com/shhac/g2g/internal/repair"
)

// stackView is the one semantic projection every command produces. It carries
// no ANSI, no writer, and no layout decisions, so the pretty renderer and the
// machine-readable formats are alternative views of identical facts rather
// than parallel implementations that can drift.
type stackView struct {
	Operation    string
	Target       string
	TargetSource string
	Nodes        []stackNode
	Action       []string
	// Blocked is the reason apply would refuse this plan. It never suppresses
	// Action: the command remains the plan's known destination, and running it
	// by hand is a legitimate way to get the external tool's own, often more
	// specific, error while triaging. It only changes how the command is
	// labelled, and it is rendered above the command so the reason is read
	// first.
	//
	// It is the reason alone. "Apply blocked" is what a person is told about
	// it and lives in BlockedHeading, because a machine reading this field was
	// being handed a label — and not even a consistent one, since status
	// prefixed its own.
	Blocked string
	// BlockedHeading is how the reason is announced: the label in front of it
	// in prose, and the heading over it when there is advice to lay out. One
	// field, because they are one thing said in two layouts.
	BlockedHeading string
	Notes          []stackNote
	// Sequence is an ordered recipe: the commands a person would run by hand to
	// reach the same place, numbered because the order is the point.
	//
	// It is separate from Action, which is one argv — the single command a plan
	// amounts to. A command whose work is a sequence has no such command, and
	// rendering only the first step of one, or joining them into something
	// unrunnable, are the two ways that goes wrong.
	Sequence []stackStep
	// Advice is the laid-out form of Blocked, rendered for a person instead of
	// it. Both are set together; only the human renderer prefers this one.
	Advice *advice
	// Repair is Blocked in the shape a consumer can act on: the reason, and
	// the ways out with their commands separate from the prose around them.
	// Blocked is rendered from it wherever one exists.
	Repair repair.Note
	// Comments are what a run writes to pull requests, body included. A
	// machine reads every one; a person is shown Excerpt instead, because
	// eight copies of nearly the same text is not a preview anyone reads.
	Comments []stackComment
	Excerpt  *stackExcerpt
}

// stackComment is one pull request's comment and what a run does with it.
type stackComment struct {
	PullRequest int
	Branch      string
	Action      string
	Reason      string
	Body        string
}

// stackExcerpt is text a person reads verbatim under a heading of its own.
type stackExcerpt struct {
	Heading string
	Lines   []string
}

// stackStep is one line of a Sequence: something runnable, and what running it
// achieves. It is repair.Step's shape without its meaning — that one is a way
// out of a refusal, this one is a step of the work itself.
type stackStep struct {
	Command string
	Effect  string
}

// severity names the meaning of a piece of output. The renderer maps it to a
// colour role; the machine-readable formats emit it verbatim.
type severity string

const (
	severityNeutral severity = "neutral"
	severityOK      severity = "ok"
	severityWarn    severity = "warn"
	severityBad     severity = "bad"
)

type stackNode struct {
	Branch   string
	Trunk    bool
	Target   bool
	PRNumber int
	PRURL    string
	// Marks are the sole annotation record. Renderers derive the combined
	// state and worst severity rather than keeping parallel cached fields.
	Marks []stackMark
	// Parent and Depth describe a forked graph. The linear commands leave both
	// zero, so their rendering is unchanged: a stack whose every node has one
	// child is a tree that happens to look like a list.
	Parent string
	Depth  int
}

// stackMark is one statement about a branch: which axis it is about, whether
// that axis is where it should be, and the words that explain it.
//
// A branch has more than one of these and they are not the same news. A pull
// request can be based exactly where it belongs and carry a commit the branch
// has never had — which is how the word "aligned" came to head a line
// describing a divergence, in a single colour that had to pick one of them.
// Each axis now says its own thing in its own colour.
type stackMark struct {
	// Subject names the axis — base, head — and is empty for a statement that
	// is not about one, such as which native GitHub stack a branch is in.
	Subject string
	// OK reports the axis is where it should be. It is read only when Subject
	// is set, because a statement about no axis is neither.
	OK bool
	// Detail explains, and is empty where the mark says everything: a base
	// that is where it belongs has nothing to add.
	Detail   string
	Severity severity
	// Plain labels stay hidden on trunks, whose glyph already names the role.
	// Supplemental marks are visible beside it.
	hideOnTrunk bool
}

// The mark and its inverse. A symbol carries further than a word at the right
// of a wide line, and these two are read as opposites without being read at
// all — which is the point, because the reader is scanning a column for the
// rows that are not fine.
const (
	markYes = "✓"
	markNo  = "✗"
)

func (m stackMark) text() string {
	if m.Subject == "" {
		return m.Detail
	}
	said := m.Subject + markNo
	if m.OK {
		said = m.Subject + markYes
	}
	if m.Detail == "" {
		return said
	}
	return said + " " + m.Detail
}

// labeled replaces the annotation with a plain label. Even an empty label
// retains an explicitly supplied severity for the machine formats.
func (n stackNode) labeled(text string, level severity) stackNode {
	n.Marks = []stackMark{{Detail: text, Severity: level, hideOnTrunk: true}}
	return n
}

// marked replaces the annotation with explicit axis or supplemental marks.
func (n stackNode) marked(marks ...stackMark) stackNode {
	n.Marks = make([]stackMark, 0, len(marks))
	for _, mark := range marks {
		if mark.text() != "" {
			n.Marks = append(n.Marks, mark)
		}
	}
	return n
}

// withMarks enriches an annotation without sharing its mutable slice. Existing
// labels become visible as part of the supplemental annotation, including on
// trunks; publication explicitly replaces a trunk's plain label instead.
func (n stackNode) withMarks(added ...stackMark) stackNode {
	marks := slices.Clone(n.Marks)
	for index := range marks {
		marks[index].hideOnTrunk = false
	}
	return n.marked(append(marks, added...)...)
}

func (n stackNode) state() string {
	said := make([]string, 0, len(n.Marks))
	for _, mark := range n.Marks {
		if text := mark.text(); text != "" {
			said = append(said, text)
		}
	}
	return strings.Join(said, "  ")
}

func (n stackNode) severity() severity {
	if n.Marks == nil {
		return ""
	}
	return worstOf(n.Marks)
}

// worstOf is the colour a reader switching on one severity should get: the
// worst thing said about the branch, because that is what they need to act on.
func worstOf(marks []stackMark) severity {
	if len(marks) == 0 {
		return severityNeutral
	}
	ranked := map[severity]int{severityOK: 0, severityNeutral: 1, severityWarn: 2, severityBad: 3}
	worst := marks[0].Severity
	for _, mark := range marks[1:] {
		if ranked[mark.Severity] > ranked[worst] {
			worst = mark.Severity
		}
	}
	return worst
}

type stackNote struct {
	Text     string
	Severity severity
}

func (v stackView) note(text string, level severity) stackView {
	v.Notes = append(v.Notes, stackNote{Text: text, Severity: level})
	return v
}

func (v stackView) block(reason string) stackView {
	v.Blocked = reason
	return v
}

// blockedBy labels a refusal. The label lived as a literal in six preview files
// and a seventh command string-replaced it back out, so changing the wording
// meant finding all seven and keeping them in step.
func (v stackView) blockedBy(reason string) stackView {
	v = v.block(reason)
	v.BlockedHeading = "Apply blocked"
	return v
}

// refusing records a refusal in both shapes at once: Blocked keeps the whole
// sentence, which is what a machine reads, and Advice is the same content laid
// out for a person, which is what makes the command it names a command rather
// than a run of words inside prose. Both come from the note, as every plan's
// own sentence does.
func (v stackView) refusing(note repair.Note) stackView {
	v = v.blockedBy(note.SentenceWith(runnable))
	v.Repair = note
	if len(note.Ways) == 0 {
		return v
	}
	laid := advice{Reason: note.Reason, Ways: note.Ways}
	v.Advice = &laid
	return v
}

// advising records a repair on a view that is not refusing anything. status
// reports a branch nothing describes as a state rather than a refusal, and the
// way out of it is the same structure and worth the same to a consumer.
func (v stackView) advising(note repair.Note) stackView {
	v.Repair = note
	return v
}

// commandHeading labels the command for what it currently is. The command is
// still shown when apply would refuse it, so the heading has to say so rather
// than inviting a copy that will not work yet.
func (v stackView) commandHeading() string {
	if v.Blocked != "" {
		return "Command to run once unblocked"
	}
	return "Command to run"
}

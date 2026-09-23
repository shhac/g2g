package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

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
	// State and Severity are what this branch's annotation says, as one string
	// and one worst-case colour. Marks is the same thing said one axis at a
	// time, and State is rendered from it wherever a command sets it.
	State    string
	Severity severity
	Marks    []stackMark
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

// marked sets the annotation from its parts. State stays the one string a
// machine reads and the worst severity stays the one colour it switches on, so
// nothing downstream has to understand marks to keep working; both are
// rendered here rather than typed out beside them.
func (n stackNode) marked(marks ...stackMark) stackNode {
	said := make([]string, 0, len(marks))
	for _, mark := range marks {
		if text := mark.text(); text != "" {
			said = append(said, text)
			n.Marks = append(n.Marks, mark)
		}
	}
	n.State, n.Severity = strings.Join(said, "  "), worstOf(n.Marks)
	return n
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
// than a run of words inside prose.
//
// The sentence is passed rather than rendered from the note, because a plan
// can be blocked by a step it delegated to — sync carries restack's refusal —
// and that one has no structure here to lay out. It must still say why.
func (v stackView) refusing(sentence string, note repair.Note) stackView {
	v = v.blockedBy(sentence)
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

// glyphs distinguish the trunk from the branches stacked on it. A projection
// onto a GitHub native stack is linear, so its vertical order is the stacking
// and a fixed indent reads more easily than an escalating one that pushed a
// deep stack off to the right and never aligned its connectors with the names
// above them.
//
// A g2g-owned graph is a tree, and a tree needs its connectors, so the fork
// glyphs below are used only when a view carries depth. A linear view renders
// exactly as it always has.
const (
	trunkGlyph  = "○"
	branchGlyph = "●"
	railGlyph   = "│"
	forkGlyph   = "├─"
	lastGlyph   = "└─"
	indent      = "  "
)

func writeStackView(writer io.Writer, view stackView, p Presentation) error {
	switch p.Format {
	case formatJSON:
		return writeJSON(writer, view)
	case formatPorcelain:
		return writePorcelain(writer, view)
	}
	lines := []string{fmt.Sprintf("%s  %s", p.accent("Target"), p.branch(view.Target))}
	if view.TargetSource != "" {
		lines[0] += "  " + p.subdued("· "+view.TargetSource)
	}
	// The graph is bounded by blank lines on both sides, and each block below
	// it is separated the same way, so no caller has to add its own spacing.
	lines = append(lines, "")
	lines = append(lines, graphLines(view, p)...)

	// Advice replaces the one-line refusal for a person, and never for a
	// machine: Blocked stays a single line so the porcelain record it becomes
	// remains one tab-separated row.
	if view.Advice != nil {
		lines = append(lines, view.Advice.lines(view.BlockedHeading, p)...)
	} else if view.Blocked != "" {
		lines = append(lines, "", p.problem(view.BlockedHeading+": "+view.Blocked))
	}
	if len(view.Action) != 0 {
		lines = append(lines, "", p.accent(view.commandHeading()), commandLine(commandText(view.Action), p))
	}
	lines = append(lines, sequenceLines(view, p)...)
	if len(view.Notes) != 0 {
		lines = append(lines, "")
	}
	for _, note := range view.Notes {
		lines = append(lines, styleBySeverity(p, note.Severity, note.Text))
	}
	if view.Excerpt != nil {
		lines = append(lines, "", p.accent(view.Excerpt.Heading))
		for _, text := range view.Excerpt.Lines {
			lines = append(lines, strings.TrimRight(indent+text, " "))
		}
	}

	_, err := io.WriteString(writer, p.drawCommands(strings.Join(lines, "\n"), "")+"\n")
	return err
}

func graphLines(view stackView, p Presentation) []string {
	prefixes := treePrefixes(view.Nodes)
	repository := viewRepository(view)
	width := 0
	for index, node := range view.Nodes {
		if size := utf8.RuneCountInString(prefixes[index] + node.Branch); size > width {
			width = size
		}
	}

	lines := make([]string, 0, len(view.Nodes)+1)
	for index, node := range view.Nodes {
		// The rail under a linear trunk is what separates the base from the
		// stack. A tree draws its own connectors, so it never needs one.
		if index > 0 && view.Nodes[index-1].Trunk && prefixes[index] == "" {
			lines = append(lines, indent+p.subdued(railGlyph))
		}
		glyph, name := p.trunk(trunkGlyph), p.trunk(node.Branch)
		if !node.Trunk {
			glyph, name = p.subdued(branchGlyph), p.branch(node.Branch)
		}
		padding := strings.Repeat(" ", width-utf8.RuneCountInString(prefixes[index]+node.Branch))
		// Styling an empty prefix would wrap nothing in escape codes, which is
		// invisible on a terminal and a diff in a golden file.
		prefix := prefixes[index]
		if prefix != "" {
			prefix = p.subdued(prefix)
		}
		line := indent + prefix + glyph + " " + name + padding
		lines = append(lines, strings.TrimRight(line+"  "+annotation(node, repository, p), " "))
	}
	return lines
}

// viewRepository is the repository this view's pull requests live in, read back
// from the first address GitHub gave us.
//
// Every node in one view belongs to the same repository, so one address answers
// for all of them. Reading it back here rather than threading it through every
// plan keeps a fallback link from becoming a field on types that otherwise have
// no interest in where a pull request is hosted.
func viewRepository(view stackView) string {
	for _, node := range view.Nodes {
		if repository := repositoryFromPullRequestURL(node.PRURL); repository != "" {
			return repository
		}
	}
	return ""
}

func annotation(node stackNode, repository string, p Presentation) string {
	if node.Trunk {
		// A trunk's marks are about something other than being one — how it
		// stands against its remote — so they follow the word rather than
		// replace it.
		parts := []string{p.subdued("trunk")}
		for _, mark := range node.Marks {
			parts = append(parts, styleBySeverity(p, mark.Severity, mark.text()))
		}
		return strings.Join(parts, "  ")
	}
	parts := make([]string, 0, 3)
	if node.PRNumber > 0 {
		number := p.pr(fmt.Sprintf("#%d", node.PRNumber))
		url := pullRequestURL(pullRequestRef{Number: node.PRNumber, URL: node.PRURL, Repository: repository})
		parts = append(parts, p.hyperlink(url, number))
	}
	switch {
	case len(node.Marks) != 0:
		for _, mark := range node.Marks {
			parts = append(parts, styleBySeverity(p, mark.Severity, mark.text()))
		}
	case node.State != "":
		parts = append(parts, styleBySeverity(p, node.Severity, node.State))
	}
	if node.Target {
		parts = append(parts, p.subdued("← target"))
	}
	return strings.Join(parts, "  ")
}

// commandLine keeps the copyable command free of any non-whitespace
// decoration. Nothing shares its line, so a loose or wrapped selection can
// only ever pick up spaces, which a shell ignores. In colour output the
// highlight is padded past the text purely to widen the click target, and by
// the same single column every other chip carries on the side a click has no
// use for.
func commandLine(command string, p Presentation) string {
	if !p.Color {
		return command
	}
	const clickTarget = 4
	return p.command(chipPadding + command + strings.Repeat(" ", clickTarget))
}

func styleBySeverity(p Presentation, level severity, text string) string {
	switch level {
	case severityOK:
		return p.aligned(text)
	case severityWarn:
		return p.divergent(text)
	case severityBad:
		return p.problem(text)
	default:
		return p.subdued(text)
	}
}

func writeReadyBanner(writer io.Writer, p Presentation) error {
	return prose(writer, p, p.accent("Ready to apply"))
}

// sequenceLines lays out a recipe, one numbered command per line with what it
// achieves beside it.
//
// The number is padded to the widest so the commands line up, because a column
// of commands that starts in two different places reads as two lists.
func sequenceLines(view stackView, p Presentation) []string {
	if len(view.Sequence) == 0 {
		return nil
	}
	heading := "Commands this would run, in order"
	if view.Blocked != "" {
		heading = "Commands this would run once unblocked"
	}
	lines := []string{"", p.accent(heading)}
	width := len(strconv.Itoa(len(view.Sequence)))
	for index, step := range view.Sequence {
		line := fmt.Sprintf("  %*d  %s", width, index+1, runnable(step.Command))
		if step.Effect != "" {
			line += "  " + p.subdued("· "+step.Effect)
		}
		lines = append(lines, line)
	}
	return lines
}

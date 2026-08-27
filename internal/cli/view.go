package cli

import (
	"fmt"
	"io"
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
	// Advice is the laid-out form of Blocked, rendered for a person instead of
	// it. Both are set together; only the human renderer prefers this one.
	Advice *advice
	// Repair is Blocked in the shape a consumer can act on: the reason, and
	// the ways out with their commands separate from the prose around them.
	// Blocked is rendered from it wherever one exists.
	Repair repair.Note
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
	State    string
	Severity severity
	// Parent and Depth describe a forked graph. The linear commands leave both
	// zero, so their rendering is unchanged: a stack whose every node has one
	// child is a tree that happens to look like a list.
	Parent string
	Depth  int
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
	if len(view.Notes) != 0 {
		lines = append(lines, "")
	}
	for _, note := range view.Notes {
		lines = append(lines, styleBySeverity(p, note.Severity, note.Text))
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
		return p.subdued("trunk")
	}
	parts := make([]string, 0, 3)
	if node.PRNumber > 0 {
		number := p.pr(fmt.Sprintf("#%d", node.PRNumber))
		url := pullRequestURL(pullRequestRef{Number: node.PRNumber, URL: node.PRURL, Repository: repository})
		parts = append(parts, p.hyperlink(url, number))
	}
	if node.State != "" {
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

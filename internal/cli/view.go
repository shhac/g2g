package cli

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
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
	Blocked string
	Notes   []stackNote
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

// commandHeading labels the command for what it currently is. The command is
// still shown when apply would refuse it, so the heading has to say so rather
// than inviting a copy that will not work yet.
func (v stackView) commandHeading() string {
	if v.Blocked != "" {
		return "Command to run once unblocked"
	}
	return "Command to run"
}

// glyphs distinguish the trunk from the branches stacked on it. Every stack
// gt2gh handles is linear — it fails closed rather than resolve a fork — so
// the vertical order is the stacking, and a fixed indent reads more easily
// than an escalating one that pushed a deep stack off to the right and never
// aligned its connectors with the names above them.
const (
	trunkGlyph  = "○"
	branchGlyph = "●"
	railGlyph   = "│"
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

	if view.Blocked != "" {
		lines = append(lines, "", p.problem(view.Blocked))
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

	_, err := io.WriteString(writer, strings.Join(lines, "\n")+"\n")
	return err
}

func graphLines(view stackView, p Presentation) []string {
	width := 0
	for _, node := range view.Nodes {
		if size := utf8.RuneCountInString(node.Branch); size > width {
			width = size
		}
	}

	lines := make([]string, 0, len(view.Nodes)+1)
	for index, node := range view.Nodes {
		if index > 0 && view.Nodes[index-1].Trunk {
			lines = append(lines, indent+p.subdued(railGlyph))
		}
		glyph, name := p.trunk(trunkGlyph), p.trunk(node.Branch)
		if !node.Trunk {
			glyph, name = p.subdued(branchGlyph), p.branch(node.Branch)
		}
		line := indent + glyph + " " + name + strings.Repeat(" ", width-utf8.RuneCountInString(node.Branch))
		lines = append(lines, strings.TrimRight(line+"  "+annotation(node, p), " "))
	}
	return lines
}

func annotation(node stackNode, p Presentation) string {
	if node.Trunk {
		return p.subdued("trunk")
	}
	parts := make([]string, 0, 3)
	if node.PRNumber > 0 {
		parts = append(parts, p.pr(fmt.Sprintf("#%d", node.PRNumber)))
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
// highlight is padded past the text purely to widen the click target.
func commandLine(command string, p Presentation) string {
	if !p.Color {
		return command
	}
	const clickTarget = 4
	return p.command(command + strings.Repeat(" ", clickTarget))
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

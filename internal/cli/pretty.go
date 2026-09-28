package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

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

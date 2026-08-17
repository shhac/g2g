package cli

import (
	"io"
	"os"

	"github.com/spf13/cobra"
)

const (
	ansiReset     = "\x1b[0m"
	ansiAccent    = "\x1b[1;36m"
	ansiNotice    = "\x1b[1;32m"
	ansiTrunk     = "\x1b[1;33m"
	ansiBranch    = "\x1b[1;37m"
	ansiPR        = "\x1b[35m"
	ansiAligned   = "\x1b[32m"
	ansiDivergent = "\x1b[1;38;5;214m"
	ansiProblem   = "\x1b[1;31m"
	ansiCommand   = "\x1b[1;97;48;5;236m"
	ansiSubdued   = "\x1b[2m"

	// OSC 8 wraps text in a hyperlink: ESC ]8;;URL ST text ESC ]8;; ST. A
	// terminal that does not implement it swallows the escape and prints the
	// text, which is why this is safe to emit whenever a human is reading.
	osc8Open  = "\x1b]8;;"
	osc8Close = "\x1b\\"
)

// Presentation carries the output decisions: whether to decorate with ANSI,
// whether text may carry a hyperlink, and which renderer consumes the view. A
// machine format never colours and never links.
type Presentation struct {
	Color bool
	// Links enables OSC 8 hyperlinks. It is separate from Color because they
	// are different capabilities answering to different things: NO_COLOR is a
	// statement about colour, and a terminal that cannot draw a link renders
	// its escape as nothing rather than as garbage. They are detected together
	// today only because the same question — is a human reading this on a
	// terminal — decides both.
	Links  bool
	Format outputFormat
}

func (p Presentation) machine() bool { return p.Format != formatPretty }

// resolve applies the run-time output flags. Presentation is built when the
// command tree is constructed, before flags are parsed, so the format has to
// be picked up here rather than at construction.
func (p Presentation) resolve(cmd *cobra.Command) Presentation {
	if useJSON, _ := cmd.Flags().GetBool("json"); useJSON {
		return Presentation{Format: formatJSON}
	}
	if usePorcelain, _ := cmd.Flags().GetBool("porcelain"); usePorcelain {
		return Presentation{Format: formatPorcelain}
	}
	if noLinks, _ := cmd.Flags().GetBool("no-links"); noLinks {
		p.Links = false
	}
	return p
}

func detectPresentation(writer io.Writer) Presentation {
	file, ok := writer.(*os.File)
	if !ok {
		return Presentation{}
	}
	info, err := file.Stat()
	terminal := err == nil && info.Mode()&os.ModeCharDevice != 0
	return Presentation{
		Color: colorEnabled(os.Getenv("NO_COLOR") != "", os.Getenv("TERM"), os.Getenv("CI") != "", terminal),
		Links: linksEnabled(os.Getenv("TERM"), os.Getenv("CI") != "", terminal),
	}
}

// linksEnabled deliberately does not consult NO_COLOR. That variable asks for
// output without colour, and a hyperlink is not colour: it adds no visible
// decoration and the text reads identically without it.
func linksEnabled(term string, ci, terminal bool) bool {
	return term != "dumb" && !ci && terminal
}

func colorEnabled(noColor bool, term string, ci bool, terminal bool) bool {
	return !noColor && term != "dumb" && !ci && terminal
}
func (p Presentation) accent(text string) string    { return p.style(ansiAccent, text) }
func (p Presentation) notice(text string) string    { return p.style(ansiNotice, text) }
func (p Presentation) trunk(text string) string     { return p.style(ansiTrunk, text) }
func (p Presentation) branch(text string) string    { return p.style(ansiBranch, text) }
func (p Presentation) pr(text string) string        { return p.style(ansiPR, text) }
func (p Presentation) aligned(text string) string   { return p.style(ansiAligned, text) }
func (p Presentation) divergent(text string) string { return p.style(ansiDivergent, text) }
func (p Presentation) problem(text string) string   { return p.style(ansiProblem, text) }
func (p Presentation) command(text string) string   { return p.style(ansiCommand, text) }
func (p Presentation) subdued(text string) string   { return p.style(ansiSubdued, text) }

// hyperlink points text at url. An empty url, a machine format, or a
// non-terminal all render the text exactly as it would have been, so a caller
// can ask for a link unconditionally and let presentation decide.
func (p Presentation) hyperlink(url, text string) string {
	if !p.Links || url == "" {
		return text
	}
	return osc8Open + url + osc8Close + text + osc8Open + osc8Close
}

func (p Presentation) style(code, text string) string {
	if !p.Color {
		return text
	}
	return code + text + ansiReset
}

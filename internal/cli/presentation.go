package cli

import (
	"io"
	"os"
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
	ansiMissing   = "\x1b[31m"
	ansiUnsafe    = "\x1b[91m"
	ansiProblem   = "\x1b[1;31m"
	ansiCommand   = "\x1b[1;97;48;5;236m"
	ansiSubdued   = "\x1b[2m"
)

type Presentation struct{ Color bool }

func detectPresentation(writer io.Writer) Presentation {
	file, ok := writer.(*os.File)
	if !ok {
		return Presentation{}
	}
	info, err := file.Stat()
	return Presentation{Color: colorEnabled(os.Getenv("NO_COLOR") != "", os.Getenv("TERM"), os.Getenv("CI") != "", err == nil && info.Mode()&os.ModeCharDevice != 0)}
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
func (p Presentation) missing(text string) string   { return p.style(ansiMissing, text) }
func (p Presentation) unsafe(text string) string    { return p.style(ansiUnsafe, text) }
func (p Presentation) problem(text string) string   { return p.style(ansiProblem, text) }
func (p Presentation) command(text string) string   { return p.style(ansiCommand, text) }
func (p Presentation) subdued(text string) string   { return p.style(ansiSubdued, text) }
func (p Presentation) style(code, text string) string {
	if !p.Color {
		return text
	}
	return code + text + ansiReset
}

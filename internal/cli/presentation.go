package cli

import (
	"io"
	"os"
)

const (
	ansiReset  = "\x1b[0m"
	ansiAccent = "\x1b[1;36m"
	ansiNotice = "\x1b[1;32m"
	ansiTrunk  = "\x1b[1;33m"
)

type Presentation struct{ Color bool }

func detectPresentation(writer io.Writer) Presentation {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return Presentation{}
	}
	file, ok := writer.(*os.File)
	if !ok {
		return Presentation{}
	}
	info, err := file.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return Presentation{}
	}
	return Presentation{Color: true}
}
func (p Presentation) accent(text string) string { return p.style(ansiAccent, text) }
func (p Presentation) notice(text string) string { return p.style(ansiNotice, text) }
func (p Presentation) trunk(text string) string  { return p.style(ansiTrunk, text) }
func (p Presentation) style(code, text string) string {
	if !p.Color {
		return text
	}
	return code + text + ansiReset
}

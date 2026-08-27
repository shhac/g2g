package cli

import "testing"

func TestColorEnabledHonorsTerminalEnvironment(t *testing.T) {
	for _, test := range []struct {
		name                        string
		noColor, ci, terminal, want bool
		term                        string
	}{
		{name: "color terminal", terminal: true, want: true},
		{name: "no color", noColor: true, terminal: true},
		{name: "continuous integration", ci: true, terminal: true},
		{name: "dumb terminal", term: "dumb", terminal: true},
		{name: "non terminal"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := colorEnabled(test.noColor, test.term, test.ci, test.terminal); got != test.want {
				t.Errorf("colorEnabled() = %t, want %t", got, test.want)
			}
		})
	}
}

// A hint is styled as a whole, and the command inside it is styled again. ANSI
// ends a style by returning to the default rather than to whatever was in
// force before, so without re-opening the enclosing style the rest of a
// subdued sentence would come back at full brightness from the first command
// onwards — which is worse than not highlighting it at all.
func TestStyledSentenceResumesAfterACommand(t *testing.T) {
	p := Presentation{Color: true}

	got := p.subdued("run " + runnable("g2g link") + " to preview a link.")

	want := ansiSubdued + "run " + ansiCommand + " g2g link " + ansiReset + ansiSubdued + " to preview a link." + ansiReset
	if got != want {
		t.Errorf("subdued hint =\n%q\nwant\n%q", got, want)
	}
}

// Marks are an instruction to the renderer, never content. Nothing that reads
// the text rather than the terminal may see one.
func TestUnstyledOutputCarriesNoMarks(t *testing.T) {
	marked := "run " + runnable("g2g track") + " to record its parent."
	want := "run g2g track to record its parent."

	for name, got := range map[string]string{
		"no color":  Presentation{}.subdued(marked),
		"machine":   plainCommands(marked),
		"undrawn":   Presentation{}.drawCommands(marked, ""),
		"redrawn":   Presentation{}.drawCommands(Presentation{}.subdued(marked), ""),
		"json note": jsonNoteText(marked),
	} {
		if got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func jsonNoteText(text string) string {
	doc := stackView{Notes: []stackNote{{Text: text}}}.document()
	return doc.Notes[0].Text
}

// The highlight is a block of background, and a block that starts on the "g"
// and ends on the "k" reads as a hard edge cutting into the command. The
// column of padding on each side is painted, so an uncoloured terminal and the
// machine formats still carry the sentence exactly as it was written.
func TestTheChipPadsOnlyWhereThereIsABackgroundToPad(t *testing.T) {
	marked := "run " + runnable("g2g link") + "."

	coloured, plain := Presentation{Color: true}, Presentation{}

	if got, want := coloured.drawCommands(marked, ""), "run "+ansiCommand+" g2g link "+ansiReset+"."; got != want {
		t.Errorf("coloured = %q, want %q", got, want)
	}
	if got, want := plain.drawCommands(marked, ""), "run g2g link."; got != want {
		t.Errorf("plain = %q, want %q", got, want)
	}
}

// Drawing runs at every write, because a caller may name a command outside
// anything it styles. A line already drawn has no marks left, so the second
// pass has to leave it exactly as it is.
func TestDrawingAnAlreadyDrawnLineChangesNothing(t *testing.T) {
	p := Presentation{Color: true}
	drawn := p.subdued("finish with " + runnable("g2g restack --continue") + ".")

	if again := p.drawCommands(drawn, ""); again != drawn {
		t.Errorf("second pass = %q, want %q", again, drawn)
	}
}

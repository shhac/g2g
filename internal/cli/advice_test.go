package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/repair"
)

func refusedView(note repair.Note) stackView {
	view := stackView{Operation: "push", Target: "synthetic-top", Nodes: []stackNode{
		{Branch: "synthetic-main", Trunk: true},
		{Branch: "synthetic-top", Target: true},
	}}
	return view.refusing(note.Sentence(), note)
}

var divergedNote = repair.Note{
	Reason: "both sides have moved on synthetic-main",
	Ways: []repair.Step{
		{Command: "g2g sync --take published", Effect: "take the published version"},
		{Effect: "reconcile it yourself"},
	},
}

// A refusal that offers a choice is where a sentence fails: the reader has to
// work out which words belong to which option, and where the command in the
// middle of them starts and ends. Laid out, each option is a line and the
// command is drawn as one.
func TestARefusalWithAChoiceIsLaidOutOnePerLine(t *testing.T) {
	var output bytes.Buffer
	if err := writeStackView(&output, refusedView(divergedNote), Presentation{}); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"Apply blocked",
		"  both sides have moved on synthetic-main",
		"  g2g sync --take published   take the published version",
		"  reconcile it yourself",
	} {
		if !strings.Contains(output.String(), want+"\n") {
			t.Errorf("no line %q in:\n%s", want, output.String())
		}
	}
}

// The command is drawn as a command and the words around it are not. Drawing
// the effect too would offer the reader something to copy that is not a
// command.
func TestOnlyTheCommandIsDrawnAsOne(t *testing.T) {
	var output bytes.Buffer
	if err := writeStackView(&output, refusedView(divergedNote), Presentation{Color: true}); err != nil {
		t.Fatal(err)
	}

	if want := ansiCommand + " g2g sync --take published " + ansiReset; !strings.Contains(output.String(), want) {
		t.Errorf("the command is not drawn as one:\n%q", output.String())
	}
	if strings.Contains(output.String(), ansiCommand+" reconcile") {
		t.Errorf("a way with no command was drawn as one:\n%q", output.String())
	}
}

// The laid-out form is for a person. A machine still gets the whole refusal in
// the one field it reads, so nothing that consumed --json before this existed
// sees a shorter answer.
func TestAMachineStillReadsTheWholeRefusalInOneField(t *testing.T) {
	var output bytes.Buffer
	if err := writeStackView(&output, refusedView(divergedNote), Presentation{Format: formatJSON}); err != nil {
		t.Fatal(err)
	}

	var doc jsonDocument
	if err := json.Unmarshal(output.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v\n%s", err, output.String())
	}
	want := "both sides have moved on synthetic-main · run g2g sync --take published to take the published version, or reconcile it yourself"
	if doc.Blocked != want {
		t.Errorf("blocked = %q, want %q", doc.Blocked, want)
	}
	// The label a person is shown in front of the reason is the renderer's,
	// and it is not even the same label everywhere: status calls the same
	// field "Safe next action". A consumer was trimming that out.
	if strings.HasPrefix(doc.Blocked, "Apply blocked") {
		t.Errorf("blocked carries the label a person is shown: %q", doc.Blocked)
	}
	if doc.Repair == nil || len(doc.Repair.Ways) != 2 {
		t.Fatalf("repair = %#v, want the two ways out", doc.Repair)
	}
	if doc.Repair.Reason != "both sides have moved on synthetic-main" {
		t.Errorf("repair reason = %q", doc.Repair.Reason)
	}
	if got := doc.Repair.Ways[0]; got.Command != "g2g sync --take published" || got.Effect != "take the published version" {
		t.Errorf("first way = %#v", got)
	}
	// A way out that is not a thing to run carries no command, rather than an
	// empty one a consumer might try to execute.
	if got := doc.Repair.Ways[1]; got.Command != "" || got.Effect != "reconcile it yourself" {
		t.Errorf("second way = %#v", got)
	}
}

// A plan can be blocked by a step it delegated to — sync carries restack's
// refusal — and that one has no structure here to lay out. It must still say
// why rather than rendering an empty heading.
func TestADelegatedRefusalStillSaysWhy(t *testing.T) {
	var output bytes.Buffer
	view := stackView{Operation: "sync", Target: "synthetic-top"}.refusing("a rewrite is already in progress", repair.Note{})
	if err := writeStackView(&output, view, Presentation{}); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(output.String(), "Apply blocked: a rewrite is already in progress") {
		t.Errorf("the delegated reason is missing:\n%s", output.String())
	}
	if strings.Contains(output.String(), "Apply blocked\n") {
		t.Errorf("an empty laid-out block was rendered:\n%s", output.String())
	}
}

package cli

import (
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/push"
)

// Every standing reads its own way, and one nobody compared says nothing at
// all rather than reading as level with the remote.
func TestEachStandingIsMarkedInItsOwnWords(t *testing.T) {
	for _, test := range []struct {
		publication push.Publication
		want        string
		level       severity
	}{
		{push.Publication{}, "", severityNeutral},
		{push.Publication{Standing: push.Current}, "origin✓", severityOK},
		{push.Publication{Standing: push.New}, "not on origin", severityNeutral},
		{push.Publication{Standing: push.Landed}, "", severityNeutral},
		{push.Publication{Standing: push.Unknown}, "origin✗ on a commit not here", severityWarn},
		{push.Publication{Standing: push.Ahead, Ours: 2}, "origin✗ 2 ahead", severityWarn},
		{push.Publication{Standing: push.Behind, Theirs: 3}, "origin✗ 3 behind", severityWarn},
		{push.Publication{Standing: push.Diverged, Ours: 1, Theirs: 2}, "origin✗ diverged · 1 here, 2 there", severityBad},
		{push.Publication{Standing: push.Rewritten, Ours: 1}, "origin✗ replayed since pushed", severityWarn},
	} {
		mark := publishedMark("origin", test.publication)
		if mark.text() != test.want || (test.want != "" && mark.Severity != test.level) {
			t.Errorf("standing %d reads %q (%s), want %q (%s)", test.publication.Standing, mark.text(), mark.Severity, test.want, test.level)
		}
	}
}

// The next steps follow the marks: push for what is only here, pull for what
// is only there, and nothing for a trunk, which is published by landing on it.
func TestPublishedNotesNameTheNextStep(t *testing.T) {
	view := stackView{Nodes: []stackNode{
		{Branch: "synthetic-main", Trunk: true},
		{Branch: "synthetic-ahead"},
		{Branch: "synthetic-behind"},
		{Branch: "synthetic-apart"},
		{Branch: "synthetic-unseen"},
		{Branch: "synthetic-uncompared"},
	}}
	notes := publishedNotes(view, "origin", map[string]push.Publication{
		"synthetic-main":   {Standing: push.Ahead, Ours: 1},
		"synthetic-ahead":  {Standing: push.Ahead, Ours: 1},
		"synthetic-behind": {Standing: push.Behind, Theirs: 1},
		"synthetic-apart":  {Standing: push.Diverged, Ours: 1, Theirs: 1},
		"synthetic-unseen": {Standing: push.Unknown},
	}).Notes
	said := ""
	for _, note := range notes {
		said += note.Text + "\n"
	}
	for _, want := range []string{
		"Not on origin as they are here: synthetic-ahead · run g2g push.",
		"origin has work synthetic-behind does not · run g2g pull.",
		"Diverged from origin: synthetic-apart",
		"has not fetched for synthetic-unseen",
	} {
		if !strings.Contains(plainCommands(said), want) {
			t.Errorf("notes do not say %q:\n%s", want, said)
		}
	}
	if strings.Contains(said, "synthetic-main") || strings.Contains(said, "synthetic-uncompared") {
		t.Errorf("notes name a trunk or a branch nobody compared:\n%s", said)
	}
}

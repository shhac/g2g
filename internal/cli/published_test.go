package cli

import (
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/push"
	"github.com/shhac/g2g/internal/shape"
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
	notes := publishedNotes(view, selected{}.from("origin"), map[string]push.Publication{
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

// A next step is aimed at what status was asked about and compared with: status
// --branch X --remote R advises pushing X to R, not the stack the reader is on
// to origin.
func TestPublishedNotesAimAtTheSelectionAndRemote(t *testing.T) {
	view := stackView{Nodes: []stackNode{{Branch: "synthetic-ahead"}, {Branch: "synthetic-behind"}}}
	acted := selected{branch: "synthetic-ahead", named: true, scope: shape.ScopeStack}.from("synthetic-upstream")
	notes := publishedNotes(view, acted, map[string]push.Publication{
		"synthetic-ahead":  {Standing: push.Ahead, Ours: 1},
		"synthetic-behind": {Standing: push.Behind, Theirs: 1},
	}).Notes
	said := ""
	for _, note := range notes {
		said += plainCommands(note.Text) + "\n"
	}
	// Only the branch itself needs publishing, and the stack's default would
	// also reach the one the remote is ahead on and be refused for it.
	for _, want := range []string{
		"run g2g push --branch synthetic-ahead --scope branch --remote synthetic-upstream.",
		"run g2g pull --branch synthetic-ahead --remote synthetic-upstream.",
	} {
		if !strings.Contains(said, want) {
			t.Errorf("notes do not say %q:\n%s", want, said)
		}
	}
}

// The push a note suggests reaches the branches that need it and no further:
// the narrowest scope around the target that covers all of them, and the
// command as status was asked when nothing narrower would select less.
func TestPublishedNotesNarrowThePushToWhatNeedsIt(t *testing.T) {
	// main ← lower ← middle ← {top, side}, and other beside lower on main.
	view := stackView{Nodes: []stackNode{
		{Branch: "synthetic-main", Trunk: true},
		{Branch: "synthetic-lower", Parent: "synthetic-main"},
		{Branch: "synthetic-middle", Parent: "synthetic-lower"},
		{Branch: "synthetic-top", Parent: "synthetic-middle"},
		{Branch: "synthetic-side", Parent: "synthetic-middle"},
		{Branch: "synthetic-other", Parent: "synthetic-main"},
	}}
	for _, test := range []struct {
		name        string
		target      string
		scope       shape.Scope
		unpublished []string
		want        string
	}{
		{name: "just the target", target: "synthetic-middle", unpublished: []string{"synthetic-middle"}, want: "g2g push --scope branch"},
		{name: "below the target", target: "synthetic-middle", unpublished: []string{"synthetic-lower", "synthetic-middle"}, want: "g2g push --scope path"},
		{name: "above the target, both arms", target: "synthetic-middle", unpublished: []string{"synthetic-top", "synthetic-side"}, want: "g2g push --scope subtree"},
		{name: "both sides", target: "synthetic-middle", unpublished: []string{"synthetic-lower", "synthetic-top"}, want: "g2g push"},
		{name: "a cousin", target: "synthetic-middle", unpublished: []string{"synthetic-other"}, want: "g2g push --scope trunk"},
		{name: "everything a leaf's path holds", target: "synthetic-top", scope: shape.ScopeStack, unpublished: []string{"synthetic-lower", "synthetic-middle", "synthetic-top"}, want: "g2g push"},
	} {
		t.Run(test.name, func(t *testing.T) {
			nodes := view.Nodes
			scope := test.scope
			if scope == "" {
				scope = shape.ScopeTrunk
			}
			if scope == shape.ScopeStack {
				// A leaf's stack is its path.
				nodes = nodes[:4]
			}
			publishing := map[string]push.Publication{}
			for _, node := range nodes {
				publishing[node.Branch] = push.Publication{Standing: push.Current}
			}
			for _, branch := range test.unpublished {
				publishing[branch] = push.Publication{Standing: push.Ahead, Ours: 1}
			}
			acted := selected{branch: test.target, scope: scope}.from("origin")
			said := ""
			for _, note := range publishedNotes(stackView{Nodes: nodes}, acted, publishing).Notes {
				said += plainCommands(note.Text) + "\n"
			}
			if !strings.Contains(said, "run "+test.want+".") {
				t.Errorf("notes do not say run %q:\n%s", test.want, said)
			}
		})
	}
}

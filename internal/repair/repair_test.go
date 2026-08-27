package repair

import "testing"

// The sentence and the laid-out form are read by different people and must not
// be able to say different things, which is why one joiner produces both. This
// pins what that joiner does with each kind of step.
func TestSentenceReadsAsAnOrderedChoice(t *testing.T) {
	for _, test := range []struct {
		name string
		note Note
		want string
	}{
		{
			name: "a reason with one command",
			note: Note{
				Reason: "the remote has moved on synthetic-top",
				Ways:   []Step{{Command: "git push --force-with-lease origin synthetic-top", Effect: "replace what is published"}},
			},
			want: "the remote has moved on synthetic-top · run git push --force-with-lease origin synthetic-top to replace what is published",
		},
		{
			// The commonest refusal offers something to do that is not a thing
			// to run, and it has to read as a whole answer rather than as an
			// effect with its command missing.
			name: "a way with no command speaks for itself",
			note: Note{
				Reason: "both sides have moved on main",
				Ways: []Step{
					{Command: "g2g sync --take published", Effect: "take the published version"},
					{Effect: "reconcile it yourself"},
				},
			},
			want: "both sides have moved on main · run g2g sync --take published to take the published version, or reconcile it yourself",
		},
		{name: "a reason with nothing to do", note: Note{Reason: "no structure source is configured"}, want: "no structure source is configured"},
		{name: "ways with no reason", note: Note{Ways: []Step{{Command: "g2g track", Effect: "record its parent"}}}, want: "run g2g track to record its parent"},
		{name: "nothing at all", note: Note{}, want: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.note.Sentence(); got != test.want {
				t.Errorf("Sentence() = %q, want %q", got, test.want)
			}
		})
	}
}

// A caller that can draw a command gets the same sentence with the commands
// marked. Decoration must not reach anything else: a reason or an effect that
// happened to be decorated would be a command the reader cannot run.
func TestDecorationReachesTheCommandsAndNothingElse(t *testing.T) {
	note := Note{
		Reason: "the graph already records a different parent for synthetic-a",
		Ways: []Step{
			{Command: "g2g untrack", Effect: "forget the recorded parent"},
			{Effect: "leave it as it is"},
		},
	}

	marked := note.SentenceWith(func(command string) string { return "[" + command + "]" })

	if want := "the graph already records a different parent for synthetic-a · run [g2g untrack] to forget the recorded parent, or leave it as it is"; marked != want {
		t.Errorf("SentenceWith() = %q, want %q", marked, want)
	}
}

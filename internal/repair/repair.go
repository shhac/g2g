// Package repair carries the way out of a refusal as structure rather than as
// a sentence.
//
// A blocked plan has always named the command that fixes it, and naming it
// inside a sentence puts a run of shell in the middle of prose: the reader has
// to work out where the command starts and ends before they can copy it. The
// presentation layer can draw a command it is handed, and cannot find one
// inside a sentence without guessing where it ends.
//
// So a package that refuses says why in its own words, and says what to do in
// parts. The two are built from the same state and can differ in shape; they
// cannot differ in which command they name. This package holds the parts, and
// depends on nothing, so a package that must not reach Graphite, GitHub or a
// network can still describe its own repair.
package repair

import "strings"

// Step is one way out of a refusal.
//
// Command is empty when the way out is not something to run — "fetch and
// reconcile first", "track it in Graphite". Those are real answers and the
// commonest reason a refusal has no command at all, so they are a Step with no
// Command rather than a separate kind of thing.
type Step struct {
	Command string
	// Effect says what the step achieves, in the reader's terms. It reads as
	// the tail of "run <command> to ...", so it starts with a verb.
	Effect string
}

// Note is a refusal in the two shapes its two readers need: why it happened,
// and the ways out. A machine gets Sentence, which is one field; a person gets
// the reason on its own line and the ways in a column, because a sentence
// listing three of them is where a reader loses which words belong to which.
//
// Both come from these values, so the two shapes cannot name different
// commands — the failure the old hand-written pair invited.
type Note struct {
	// Reason says what state the caller is in, without advice. It is empty
	// when the ways say everything there is to say.
	Reason string
	Ways   []Step
}

// Sentence is the one-line form: the reason, then what to do about it.
func (n Note) Sentence() string { return n.SentenceWith(nil) }

// SentenceWith is Sentence with every command passed through decorate, for a
// caller that can draw one and wants the sentence rather than the column.
//
// It takes the decoration rather than exposing the joining, because the joining
// is the thing that must not exist twice: a caller assembling its own would be
// free to word it differently from the one a machine reads.
func (n Note) SentenceWith(decorate func(string) string) string {
	ways := sentence(n.Ways, decorate)
	switch {
	case n.Reason == "":
		return ways
	case ways == "":
		return n.Reason
	}
	return n.Reason + " · " + ways
}

// Sentence renders the steps as one line, for a reader who gets one line: a
// machine field, or stderr, where there is no column to lay them out in.
//
// It exists so the sentence and the laid-out form cannot name different
// commands. Where a package needs different wording it composes its own — what
// must not happen is the two being typed out separately and drifting.
func Sentence(steps []Step) string { return sentence(steps, nil) }

func sentence(steps []Step, decorate func(string) string) string {
	if decorate == nil {
		decorate = func(command string) string { return command }
	}
	parts := make([]string, 0, len(steps))
	for _, step := range steps {
		if step.Command == "" {
			parts = append(parts, step.Effect)
			continue
		}
		parts = append(parts, "run "+decorate(step.Command)+" to "+step.Effect)
	}
	return strings.Join(parts, ", or ")
}

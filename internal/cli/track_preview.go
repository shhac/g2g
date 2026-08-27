package cli

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/shhac/g2g/internal/graph"
)

func trackView(plan graph.TrackPlan, describedElsewhere bool) stackView {
	view := driftNotes(graphView(plan.Discovery, "track"), plan.Discovery)
	if plan.Blocked == "" {
		view = view.note(fmt.Sprintf("Records %s under %s.", plan.Target, plan.Parent), severityOK)
		if plan.NewTrunk != "" {
			view = view.note(fmt.Sprintf("%s becomes a root of the graph.", plan.NewTrunk), severityNeutral)
		}
		return view.note(confirmation(plan), severityFor(plan))
	}
	view = view.blockedBy(plan.Blocked)
	for _, note := range candidateNotes(plan) {
		view = view.note(note.Text, note.Severity)
	}
	// Another record may already hold the answer this is asking for. track
	// cannot read it — that independence is the point — but it can say so, and
	// name the command that can, which is usually the shorter road.
	if describedElsewhere && plan.Parent == "" {
		for _, note := range alignedCommands([2]string{"g2g import", "adopt what Graphite already records"}) {
			view = view.note(note.Text, note.Severity)
		}
	}
	return view
}

// confirmation says whether Git already agrees with the edge being recorded.
// A parent whose commits are not in the branch is not an error — it is how a
// stack looks before it is restacked — but recording it silently would hide
// the one fact that explains why.
func confirmation(plan graph.TrackPlan) string {
	if plan.Updated.Edges[plan.Target].Origin == graph.OriginAncestry {
		return "Commit ancestry confirms " + plan.Parent + " is already below " + plan.Target + "."
	}
	return plan.Parent + " is not an ancestor of " + plan.Target + " · the edge is recorded as asserted, and " + plan.Target + " will read as needing a restack."
}
func severityFor(plan graph.TrackPlan) severity {
	if plan.Updated.Edges[plan.Target].Origin == graph.OriginAncestry {
		return severityNeutral
	}
	return severityWarn
}

// plausibleCandidates is how many of the ordered candidates are worth reading.
//
// Every ancestor branch is a candidate, so a long-lived repository offers
// dozens, and one tens of thousands of commits behind is not a parent anybody
// is choosing between. Listing them all buried the two that mattered inside a
// paragraph.
const plausibleCandidates = 3

// candidateNotes names the choice rather than making it, one line at a time.
//
// The nearest ancestor is usually the parent, and "usually" is not a basis for
// writing down a structure every later command trusts — so the likeliest is
// shown as the likeliest, and the commands that decide sit on their own lines
// rather than trailing a sentence.
func candidateNotes(plan graph.TrackPlan) []stackNote {
	if len(plan.Candidates) == 0 {
		return []stackNote{{Text: "No candidate parent found · pass --parent with a local branch to record one.", Severity: severityNeutral}}
	}

	nearest := plan.Candidates[0]
	notes := []stackNote{{
		Text:     "Nearest ancestor: " + nearest.Branch + describeCandidate(nearest),
		Severity: severityNeutral,
	}}
	if others := plan.Candidates[1:]; len(others) != 0 {
		notes = append(notes, stackNote{Text: "Then: " + describeOthers(others), Severity: severityNeutral})
	}
	return append(notes, alignedCommands(
		[2]string{"g2g track --parent " + nearest.Branch, "record just this edge"},
		[2]string{"g2g track --stack", "record the whole ancestry at once"},
	)...)
}

// alignedCommands pads each command to a common width so what they do lines up
// in its own column. The commands vary in length with a branch name, so the
// width is computed rather than guessed.
//
// The padding sits outside the mark, so what is drawn as runnable is the
// command alone and the column it lines up in is unaffected.
func alignedCommands(commands ...[2]string) []stackNote {
	width := 0
	for _, command := range commands {
		if size := utf8.RuneCountInString(command[0]); size > width {
			width = size
		}
	}
	notes := make([]stackNote, 0, len(commands))
	for _, command := range commands {
		padding := strings.Repeat(" ", width-utf8.RuneCountInString(command[0]))
		notes = append(notes, stackNote{Text: runnable(command[0]) + padding + "   " + command[1], Severity: severityNeutral})
	}
	return notes
}

// describeOthers lists the next few and counts the rest, so the tail is
// acknowledged rather than either dumped or silently dropped.
func describeOthers(others []graph.Candidate) string {
	shown := others
	if len(shown) > plausibleCandidates {
		shown = shown[:plausibleCandidates]
	}
	described := make([]string, 0, len(shown))
	for _, candidate := range shown {
		described = append(described, candidate.Branch+describeCandidate(candidate))
	}
	listed := strings.Join(described, ", ")
	if remaining := len(others) - len(shown); remaining > 0 {
		listed += fmt.Sprintf(", and %s further back", count(remaining, "other", "others"))
	}
	return listed
}

// stackView renders a whole-stack adoption. It is the same graph view every
// other structure command uses; only the notes differ.
func trackStackView(plan graph.StackPlan) stackView {
	view := driftNotes(graphView(plan.Discovery, "track"), plan.Discovery)
	if plan.Blocked != "" {
		return view.blockedBy(plan.Blocked)
	}
	if len(plan.Record) == 0 {
		return view.note("The graph already records this whole ancestry.", severityNeutral)
	}
	view = view.note(fmt.Sprintf("Records %s, from %s upwards.", branchList(plan.Branches()), plan.Trunk), severityOK)
	for _, adoption := range plan.Record {
		view = view.note(fmt.Sprintf("  %s under %s", adoption.Branch, adoption.Parent), severityNeutral)
	}
	if plan.NewTrunk != "" {
		view = view.note(fmt.Sprintf("%s becomes a root of the graph.", plan.NewTrunk), severityNeutral)
	}
	if len(plan.Already) != 0 {
		view = view.note("Already recorded: "+branchList(plan.Already)+".", severityNeutral)
	}
	return view
}
func describeCandidate(candidate graph.Candidate) string {
	described := count(candidate.Distance, "commit", "commits") + " behind"
	if candidate.Trunk {
		described = "root, " + described
	}
	return " (" + described + ")"
}

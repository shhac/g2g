package cli

import (
	"fmt"
	"slices"

	"github.com/shhac/g2g/internal/align"
)

// adoptView leads with the authority claim rather than the branch count.
//
// Listing what would be adopted understates what happens: afterwards g2g
// decides for every one of those branches, and --from on a read is the only
// way to see the other record's answer again. That is the part worth reading
// before typing --apply.
func adoptView(plan align.AdoptPlan) stackView {
	source := importedFrom(plan.From)
	view := stackView{Operation: plan.From + " adopt", Target: source.name, TargetSource: "source"}
	if plan.Blocked != "" {
		if len(plan.Conflicts) != 0 {
			view = view.note(conflictNote(plan, source), severityBad)
		}
		return view.refusing(plan.Blocked, plan.Repair)
	}
	// Nothing-to-adopt is applyFlow's line to say, not this view's.
	if len(plan.Adopt) == 0 {
		return agreementNote(view, plan)
	}
	view = view.note("Adopts "+branchList(plan.Claims())+" into the g2g graph.", severityOK)
	view = view.note(source.authority(len(plan.Adopt)), severityWarn)
	if len(plan.NewTrunks) != 0 {
		view = view.note("Records "+branchList(plan.NewTrunks)+" as "+pick(len(plan.NewTrunks), "a trunk", "trunks")+" of the g2g forest.", severityNeutral)
	}
	for _, note := range unconfirmedNotes(plan) {
		view = view.note(note, severityWarn)
	}
	return agreementNote(view, plan)
}

// adoptSource is how a preview speaks about the record an adoption read.
type adoptSource struct {
	name string
	// says is how a conflict names this record's side of it.
	says string
	// authority says what adoption changes, and how to see this record again.
	authority func(count int) string
}

func importedFrom(from string) adoptSource {
	if from == align.FromGitHub {
		return adoptSource{
			name: align.FromGitHub,
			says: "the pull request says",
			authority: func(count int) string {
				them := pick(count, "it", "them")
				return fmt.Sprintf("g2g answers for %s from now on · the pull requests are unchanged, and %s still shows what GitHub will merge.",
					them, runnable("g2g github status --from github"))
			},
		}
	}
	return adoptSource{
		name: align.FromGraphite,
		says: "Graphite says",
		authority: func(count int) string {
			them := pick(count, "it", "them")
			return fmt.Sprintf("g2g answers for %s from now on · run %s to see Graphite's view of %s. Graphite keeps tracking %s.",
				them, runnable("g2g status --from graphite"), them, them)
		},
	}
}

func agreementNote(view stackView, plan align.AdoptPlan) stackView {
	if len(plan.Agreed) == 0 {
		return view
	}
	return view.note("Both already agree about "+branchList(plan.Agreed)+".", severityNeutral)
}

// unconfirmedNotes says which adopted edges Git does not yet show, in track's
// words: it is the same state, reached the same way, and a stack that reads as
// needing a restack straight after an adoption should not be a surprise.
func unconfirmedNotes(plan align.AdoptPlan) []string {
	notes := make([]string, 0, len(plan.Unconfirmed))
	for _, adoption := range plan.Adopt {
		if !slices.Contains(plan.Unconfirmed, adoption.Branch) {
			continue
		}
		notes = append(notes, adoption.Parent+" is not an ancestor of "+adoption.Branch+" · the edge is recorded as asserted, and "+adoption.Branch+" will read as needing a restack.")
	}
	return notes
}

// conflictNote names each disagreement in full. "Blocked on a conflict" is not
// actionable; which parent each record holds is.
func conflictNote(plan align.AdoptPlan, source adoptSource) string {
	note := "The two records disagree about " + branchList(conflictedBranches(plan)) + ":"
	for _, conflict := range plan.Conflicts {
		note += fmt.Sprintf("\n  %s · g2g says %s, %s %s", conflict.Branch, conflict.Ours, source.says, conflict.Theirs)
	}
	return note
}

func conflictedBranches(plan align.AdoptPlan) []string {
	names := make([]string, 0, len(plan.Conflicts))
	for _, conflict := range plan.Conflicts {
		names = append(names, conflict.Branch)
	}
	return names
}

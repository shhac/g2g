package link

import (
	"fmt"
	"strings"

	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/stack"
)

// Repair is what to do about a blocked plan, and which kinds of issue it
// answers — the branches a person is shown under it.
//
// It is decided here, once. It used to be two parallel cascades in the
// presentation layer, one for the sentence a machine reads and one for the
// column a person reads, and they had drifted into agreeing with each other
// while both being wrong: a wrong base was sent to sync, which never touches a
// pull request, and a merged branch to Graphite whichever record described the
// stack.
//
// The order is the order the cases have to be answered in. Merged and landed
// come first because a pull request opened or retargeted for work already in
// the trunk is the wrong next step, whatever else is also true.
func (p Plan) Repair() (repair.Note, []IssueKind) {
	if len(p.Issues) == 0 {
		return repair.Note{}, nil
	}
	if merged := p.MergedBranches(); len(merged) != 0 {
		return repair.Note{Reason: sentenceList(merged) + " already merged", Ways: []repair.Step{bringDown(p.Source)}}, []IssueKind{IssueMerged}
	}
	if landed := p.LandedBranches(); len(landed) != 0 {
		return repair.Note{Reason: sentenceList(landed) + pick(len(landed), " has", " have") + " already landed", Ways: []repair.Step{ForgetLanded(p.Source)}}, []IssueKind{IssueLanded}
	}
	if p.allIssuesAre(IssueBase) {
		return repair.Note{
			Reason: "every pull request is open, and " + pick(len(p.Issues), "one is", "some are") + " based on the wrong branch",
			Ways:   []repair.Step{{Command: "g2g github retarget", Effect: "point each pull request at the branch below it"}},
		}, []IssueKind{IssueBase}
	}
	if p.allIssuesAre(IssueMissing, IssueClosed) {
		return repair.Note{
			Reason: p.missingReason(),
			Ways:   []repair.Step{{Command: "g2g submit", Effect: pick(len(p.Issues), "open a new pull request", fmt.Sprintf("open a new pull request for each of these %d branches", len(p.Issues)))}},
		}, []IssueKind{IssueMissing, IssueClosed}
	}
	return repair.Note{Ways: []repair.Step{{Effect: "resolve every unresolved GitHub PR mapping first"}}}, nil
}

// bringDown is what brings a stack past a pull request that merged, which
// depends on which record describes it. g2g's own graph is advanced and
// replayed by sync; what then reads as landed is prune's, and status says so
// next. A Graphite-described stack is Graphite's to restack. A structure read
// from pull request bases is not a record anything here edits.
func bringDown(source stack.Source) repair.Step {
	switch source {
	case stack.SourceG2G:
		return repair.Step{Command: "g2g pull", Effect: "advance the trunk and replay the branches above onto it"}
	case stack.SourceGraphite:
		return repair.Step{Command: "gt sync", Effect: "restack in Graphite around the branches that merged"}
	}
	return repair.Step{Effect: "update the trunk and restack wherever this stack is recorded · nothing here holds it"}
}

// ForgetLanded names what removes a branch whose work is already below it,
// which depends on which record answered. prune edits g2g's own graph, so it
// would find nothing to forget in a repository whose structure Graphite
// declares — and a structure read from pull request bases is not a record
// anything here edits at all.
func ForgetLanded(source stack.Source) repair.Step {
	switch source {
	case stack.SourceGraphite:
		return repair.Step{Command: "gt sync", Effect: "restack in Graphite around the branches that have landed"}
	case stack.SourceG2G:
		return repair.Step{Command: "g2g prune", Effect: "forget the branches whose work is already below them"}
	}
	return repair.Step{Effect: "remove them wherever their structure is recorded · nothing here holds it"}
}

func (p Plan) missingReason() string {
	var missing, closed []string
	for _, issue := range p.Issues {
		if issue.Kind == IssueClosed {
			closed = append(closed, issue.Branch)
			continue
		}
		missing = append(missing, issue.Branch)
	}
	switch {
	case len(closed) == 0:
		return sentenceList(missing) + pick(len(missing), " has", " have") + " no pull request"
	case len(missing) == 0:
		return sentenceList(closed) + pick(len(closed), " had its pull request closed", " had their pull requests closed")
	default:
		return sentenceList(missing) + pick(len(missing), " has", " have") + " no pull request, and " + sentenceList(closed) + pick(len(closed), " had one closed", " had theirs closed")
	}
}

// sentenceList names branches as the subject of a sentence.
func sentenceList(branches []string) string {
	switch len(branches) {
	case 0:
		return ""
	case 1:
		return branches[0]
	case 2:
		return branches[0] + " and " + branches[1]
	}
	return strings.Join(branches[:len(branches)-1], ", ") + " and " + branches[len(branches)-1]
}

func pick(count int, one, many string) string {
	if count == 1 {
		return one
	}
	return many
}

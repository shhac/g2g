package cli

import (
	"fmt"
	"strings"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/link"
	"github.com/shhac/g2g/internal/stack"
)

func linkView(plan link.Plan) stackView {
	issues := make(map[string]string, len(plan.Issues))
	for _, issue := range plan.Issues {
		issues[issue.Branch] = issue.Reason
	}
	prs := githubstack.ByHead(plan.PullRequests)

	view := stackView{
		Operation:    "link",
		Target:       plan.Target,
		TargetSource: plan.TargetSource,
		Nodes:        []stackNode{{Branch: plan.Base, Trunk: true}},
	}
	for _, branch := range plan.Branches {
		node := stackNode{Branch: branch, Target: branch == plan.Target, PRNumber: prs[branch].Number, PRURL: prs[branch].URL}
		// The marker keeps an unresolved node self-describing without colour,
		// so redirected output still says why a branch cannot be linked.
		if reason := issues[branch]; reason != "" {
			node.PRNumber, node.State, node.Severity = 0, "unresolved: "+reason, severityBad
		}
		view.Nodes = append(view.Nodes, node)
	}

	// A command is withheld only when none can be constructed: gh stack link
	// needs at least two branches. Being blocked is not that case — the command
	// is well formed, and showing it keeps the plan's destination visible for
	// triage, including running it by hand to see gh's own error.
	//
	// The count is checked directly rather than through NothingToLink, which
	// also folds in the issue check: a single-branch path that was blocked
	// therefore used to render a one-branch gh stack link that could never be
	// valid.
	if len(plan.Branches) >= 2 {
		view.Action = append([]string{"gh", "stack", "link", "--base", plan.Base}, plan.Branches...)
	}
	if len(plan.Issues) != 0 {
		return view.blockedBy(blockedReason(plan))
	}
	if len(view.Action) == 0 {
		return view.note("Nothing to link — this stack has one pull request.", severityNeutral)
	}
	return view
}

// blockedReason names the command that actually repairs the state. A blocked
// preview that only says "resolve the mappings" leaves the reader to work out
// which of five commands — or which Graphite command — applies, and the plan
// already knows.
//
// It returns the reason without a label. status wants the same sentence under a
// different heading, and it used to get one by string-replacing the prefix back
// out of a rendered line — which quietly tied one command's output to a literal
// six other files typed by hand.
func blockedReason(plan link.Plan) string {
	// Merged branches come first: they are the only case no g2g command
	// fixes. The stack itself is stale, and Graphite has to restack around
	// them before anything here can help.
	if merged := plan.MergedBranches(); len(merged) != 0 {
		return fmt.Sprintf("%s already merged. Run %s in Graphite to restack, then re-run.", branchList(merged), runnable("gt sync"))
	}
	if landed := plan.LandedBranches(); len(landed) != 0 {
		return branchList(landed) + pick(len(landed), " has", " have") + " already landed. " + forgetSentence(plan.Source)
	}
	if plan.SyncRepairable() {
		return "every pull request is open but based on the wrong branch. Run " + runnable("g2g sync") + " to preview reconciling them."
	}
	if plan.SubmitRepairable() {
		return submitAdvice(plan) + " Run " + runnable("g2g submit") + " to create " + pick(len(plan.Issues), "it", "them") + "."
	}
	return "resolve every unresolved GitHub PR mapping first."
}

// forgetSentence says the same thing forgetLanded lays out, for the one line a
// machine reads. Both come from the same step, so they cannot name different
// commands.
func forgetSentence(source stack.Source) string {
	way := forgetLanded(source)
	if way.Command == "" {
		return way.Effect + "."
	}
	return "Run " + runnable(way.Command) + " to " + way.Effect + "."
}

func submitAdvice(plan link.Plan) string {
	var missing, closed []string
	for _, issue := range plan.Issues {
		if issue.Kind == link.IssueClosed {
			closed = append(closed, issue.Branch)
			continue
		}
		missing = append(missing, issue.Branch)
	}
	switch {
	case len(closed) == 0:
		return branchList(missing) + pick(len(missing), " has", " have") + " no pull request."
	case len(missing) == 0:
		return branchList(closed) + " had its pull request closed."
	default:
		return branchList(missing) + pick(len(missing), " has", " have") + " no pull request, and " + branchList(closed) + " had one closed."
	}
}

// branchList renders one or more branch names as a readable subject.
func branchList(branches []string) string {
	switch len(branches) {
	case 1:
		return branches[0]
	case 2:
		return branches[0] + " and " + branches[1]
	default:
		return strings.Join(branches[:len(branches)-1], ", ") + " and " + branches[len(branches)-1]
	}
}

func commandText(command []string) string {
	parts := make([]string, len(command))
	for index, argument := range command {
		parts[index] = shellQuote(argument)
	}
	return strings.Join(parts, " ")
}

// shellQuote leaves an argument alone when every rune in it is safe, and quotes
// it otherwise.
//
// The condition used to be the negation of a character class, inverted again by
// IndexFunc, and compared against < 0 — three negations to say "all of these
// are safe", on the path that renders a command the reader is invited to paste.
func shellQuote(argument string) string {
	if argument != "" && !strings.ContainsFunc(argument, func(r rune) bool { return !shellSafe(r) }) {
		return argument
	}
	return "'" + strings.ReplaceAll(argument, "'", "'\\''") + "'"
}

// shellSafe is the set of runes a POSIX shell passes through untouched, stated
// positively so it can be read and tested as a list rather than inverted.
func shellSafe(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	default:
		return strings.ContainsRune("_+-./:=@", r)
	}
}

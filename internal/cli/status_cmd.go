package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/link"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
	"github.com/spf13/cobra"
)

func newStatus(service link.Service, completions stack.Completions, presentation Presentation) *cobra.Command {
	var selection stackOptions
	cmd := &cobra.Command{Use: "status", GroupID: groupPublish, Short: "Inspect a stack, its pull requests, and native GitHub membership (read-only)", Args: cobra.NoArgs}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		presentation := presentation.resolve(cmd)
		if err := selection.validate(); err != nil {
			return err
		}
		ctx, cancel := newBudgets(cmd).discovery(commandContext(cmd.Context(), cmd, "status", "read_only", selection.branch, selection.trunk))
		defer cancel()
		plan, err := service.Plan(ctx, selection.Selection())
		if err != nil {
			// "Nothing is stacked here" is an answer to what status was asked,
			// not a failure to answer it. Refusing meant the read-only triage
			// entry point exited non-zero on a repository that is simply not
			// stacked yet — while graph rendered the very same fact and exited
			// zero. Only this command renders it; anything that mutates still
			// has nothing to act on and still refuses.
			var undescribed stack.Undescribed
			if errors.As(err, &undescribed) {
				return writeUnstacked(cmd.OutOrStdout(), undescribed, presentation)
			}
			return err
		}
		return writeStatus(cmd.OutOrStdout(), plan, presentation)
	}
	selection.register(cmd, completions, stack.ReadableSources, "local branch to inspect (defaults to current branch)", "trunk to use as the base")
	// status reads, so it defaults to the whole stack: ancestors, descendants,
	// and where the target sits between them. It stops short of all, because a
	// repository's other trunks are not what someone triaging this one asked
	// about.
	selection.registerScope(cmd, shape.Scopes, stack.ScopeStack, scopeUsage("show", shape.Scopes))
	return cmd
}

// membershipView is the graph both status and unlink render: the selected path
// with each branch's pull request and native-stack membership. They differ only
// in the notes below it, so unlink composes this rather than building status's
// whole projection and discarding half of it.
func membershipView(plan link.Plan, operation string) (stackView, githubstack.Membership) {
	issues := map[string]link.Issue{}
	for _, issue := range plan.Issues {
		issues[issue.Branch] = issue
	}
	native := githubstack.AssessMembership(plan.Branches, plan.PullRequests)

	// The base is a node too, and it is the parent of every top-level branch, so
	// it has to be in the ordered selection for depth to come out right.
	depths := treeDepths(append([]string{plan.Base}, plan.Branches...), plan.ParentOf)
	view := stackView{
		Operation:    operation,
		Target:       plan.Target,
		TargetSource: plan.TargetSource,
		Nodes:        []stackNode{{Branch: plan.Base, Trunk: true}},
	}
	absent := map[string]bool{}
	for _, branch := range plan.Snapshot.Absent {
		absent[branch] = true
	}
	for _, branch := range plan.Branches {
		node := stackNode{Branch: branch, Target: branch == plan.Target, Parent: plan.Parents[branch], Depth: depths[branch]}
		// A branch this checkout does not have is structure and not a place to
		// act, so it is marked as what it is rather than as a blocked branch:
		// nothing here is wrong with it, and no command will touch it.
		if absent[branch] {
			view.Nodes = append(view.Nodes, node.marked(stackMark{Detail: "on remote only", Severity: severityNeutral}))
			continue
		}
		if issue, blocked := issues[branch]; blocked {
			view.Nodes = append(view.Nodes, node.marked(issueMark(issue)))
			continue
		}
		pr := native.Branches[branch]
		node.PRNumber, node.PRURL = pr.Number, pr.URL
		// Two axes, said separately. A base is where it belongs or it is not,
		// and that is silent about whether the pull request has the work
		// sitting in the branch: one line used to say "aligned" and then
		// describe a divergence, in whichever colour the worse of them won.
		marks := []stackMark{{Subject: "base", OK: true, Severity: severityOK}}
		if note, level := currencyNote(plan.Currency, branch); note != "" {
			marks = append(marks, stackMark{Subject: "head", Detail: note, Severity: level})
		}
		// Which native stack a branch is in is a third thing, and it used to
		// replace both of the above rather than join them — so a diverged head
		// went unsaid on exactly the branches something else was also wrong
		// with.
		if marker, markerSeverity := membershipStyle(native, branch, plan.Forks()); marker != "" {
			marks = append(marks, stackMark{Detail: marker, Severity: markerSeverity})
		}
		view.Nodes = append(view.Nodes, node.marked(marks...))
	}
	return view, native
}

func statusView(plan link.Plan) stackView {
	view, native := membershipView(plan, "status")
	if len(plan.Issues) != 0 {
		// The same reason every mutating command refuses on, under the heading
		// a read-only report gives it. The heading used to be concatenated in
		// here and string-replaced back out elsewhere, which is why it is a
		// field: only its wording differs from a refusal's.
		blocked := view.block(blockedReason(plan))
		blocked.BlockedHeading = "Safe next action"
		laid := repairAdvice(plan)
		blocked.Advice = &laid
		return structureNote(blocked, plan.Snapshot)
	}
	return structureNote(view.note(nativeMessage(native), membershipNoteSeverity(native.State)), plan.Snapshot)
}

// structureNote says which record described this stack, and how much of it is
// on screen.
//
// Structure is resolved per branch and never stored, so which record answered
// is a property of this invocation rather than of the repository. Leaving it
// unsaid was tolerable while Graphite was the only possible answer; it stops
// being tolerable the moment two records can disagree and the reader cannot
// tell which one they are looking at.
func structureNote(view stackView, snapshot stack.Snapshot) stackView {
	source := string(snapshot.Source)
	if source == "" {
		source = "unresolved"
	}
	shown := len(snapshot.Branches) + 1
	return view.note(fmt.Sprintf("Structure from %s · scope %s · %s", source, snapshot.Scope, count(shown, "branch", "branches")), severityNeutral)
}

func writeStatus(writer io.Writer, plan link.Plan, p Presentation) error {
	return writeStackView(writer, statusView(plan), p)
}

// membershipStyle answers everything a renderer needs about one branch's
// native-stack membership in a single place. Three functions switching on the
// same state meant its presentation was spread across three switches that had
// to agree.
func membershipStyle(membership githubstack.Membership, branch string, forked bool) (marker string, nodeSeverity severity) {
	pr := membership.Branches[branch]
	switch membership.State {
	case githubstack.Partial:
		if pr.StackNumber == 0 {
			return "not linked", severityWarn
		}
	case githubstack.Conflicting:
		if pr.StackNumber == 0 {
			return "not linked", severityBad
		}
		return fmt.Sprintf("stack #%d, position %d", pr.StackNumber, pr.StackPosition), severityBad
	}
	// A GitHub stack is linear and a selection need not be, so where the two
	// overlap the members are marked inside the tree. Without it the reader is
	// shown a shape and told a stack number, and has to work out for themselves
	// which branches the number covers.
	if forked && pr.StackNumber != 0 {
		return fmt.Sprintf("stack #%d · %d/%d", pr.StackNumber, pr.StackPosition, membership.StackSize), severityOK
	}
	return "", severityNeutral
}

// issueMark says which axis a refusal is about. A pull request based on the
// wrong branch is the inverse of base✓ and says so; one that does not exist,
// was closed, or is two of them, is not a statement about a base at all, and
// marking it as one would answer a question nobody asked.
func issueMark(issue link.Issue) stackMark {
	if issue.Kind == link.IssueBase {
		return stackMark{Subject: "base", Detail: issue.Reason, Severity: severityBad}
	}
	return stackMark{Subject: "pr", Detail: issue.Reason, Severity: severityBad}
}

func membershipNoteSeverity(state githubstack.MembershipState) severity {
	if state == githubstack.Conflicting {
		return severityBad
	}
	return severityNeutral
}

func nativeMessage(s githubstack.Membership) string {
	switch s.State {
	case githubstack.Aligned:
		return fmt.Sprintf("GitHub stack #%d · selected path %d/%d · aligned", s.StackNumber, s.Selected, s.StackSize)
	case githubstack.Partial:
		return fmt.Sprintf("GitHub stack #%d · partial (%d/%d linked) · run %s to add the marked PRs.", s.StackNumber, s.Linked, s.Selected, runnable("g2g link"))
	case githubstack.Conflicting:
		return "GitHub stack: conflicting membership · review the marked PRs before changing anything."
	default:
		return "GitHub stack: not linked · run " + runnable("g2g link") + " to preview a link."
	}
}

// writeUnstacked reports a branch no source places, as a state rather than a
// refusal.
//
// It goes through the same view every other status output does, so --json and
// --porcelain keep describing the world in one shape: a target, no branches,
// and a note saying why. A consumer that switched on the exit code to mean
// "there is a stack" was reading the wrong thing; the branch list says it.
func writeUnstacked(writer io.Writer, undescribed stack.Undescribed, p Presentation) error {
	view := stackView{
		Operation:    "status",
		Target:       undescribed.Branch,
		TargetSource: "current Git branch",
		Nodes:        []stackNode{{Branch: undescribed.Branch, Trunk: undescribed.Trunk, Target: true, State: unstackedState(undescribed), Severity: severityNeutral}},
	}
	// The remedy goes out as structure as well as prose: this is the commonest
	// moment someone asks what to run, and a consumer had to parse the note to
	// find out.
	view = view.advising(undescribed.Remedy()).note(unstackedNote(undescribed), severityNeutral)
	return writeStackView(writer, view, p)
}

func unstackedState(undescribed stack.Undescribed) string {
	if undescribed.Trunk {
		return "trunk · nothing stacked on it"
	}
	return "untracked"
}

func unstackedNote(undescribed stack.Undescribed) string {
	if undescribed.Trunk {
		return fmt.Sprintf("%s is this repository's default branch and nothing is stacked on it yet · start one with %s.", undescribed.Branch, runnable("g2g track --branch <child> --parent "+undescribed.Branch))
	}
	return undescribed.Sentence(runnable)
}

// currencyNote says how a branch stands against the commit its pull request is
// on. A current one gets no note: it is the ordinary case, and annotating it
// would bury the two that are not.
func currencyNote(currency map[string]link.Currency, branch string) (string, severity) {
	state, compared := currency[branch]
	if !compared || state.Current() {
		return "", severityOK
	}
	if state.Diverged {
		if state.Unpushed > 0 {
			return fmt.Sprintf("PR is on a commit this branch does not have, and %s here are not on it", count(state.Unpushed, "commit", "commits")), severityBad
		}
		return "PR is on a commit this branch does not have", severityBad
	}
	return fmt.Sprintf("%s not pushed", count(state.Unpushed, "commit", "commits")), severityWarn
}

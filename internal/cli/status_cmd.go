package cli

import (
	"fmt"
	"io"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/link"
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
			return err
		}
		return writeStatus(cmd.OutOrStdout(), plan, presentation)
	}
	selection.register(cmd, completions, stack.ReadableSources, "local branch to inspect (defaults to current branch)", "trunk to use as the base")
	// status reads, so it defaults to the whole stack: ancestors, descendants,
	// and where the target sits between them. It stops short of all, because a
	// repository's other trunks are not what someone triaging this one asked
	// about.
	selection.registerScope(cmd, stack.Scopes, stack.ScopeStack, scopeUsage("show", stack.Scopes))
	return cmd
}

// membershipView is the graph both status and unlink render: the selected path
// with each branch's pull request and native-stack membership. They differ only
// in the notes below it, so unlink composes this rather than building status's
// whole projection and discarding half of it.
func membershipView(plan link.Plan, operation string) (stackView, githubstack.Membership) {
	issues := map[string]string{}
	for _, issue := range plan.Issues {
		issues[issue.Branch] = issue.Reason
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
	for _, branch := range plan.Branches {
		node := stackNode{Branch: branch, Target: branch == plan.Target, Parent: plan.Parents[branch], Depth: depths[branch]}
		if reason := issues[branch]; reason != "" {
			node.State, node.Severity = "blocked: "+reason, severityBad
			view.Nodes = append(view.Nodes, node)
			continue
		}
		pr := native.Branches[branch]
		node.PRNumber, node.PRURL = pr.Number, pr.URL
		node.State, node.Severity = "aligned", severityOK
		if marker, markerSeverity := membershipStyle(native, branch, plan.Forks()); marker != "" {
			node.State, node.Severity = marker, markerSeverity
		}
		view.Nodes = append(view.Nodes, node)
	}
	return view, native
}

// statusAdvice reuses the blocked-reason logic so triage and the command that
// fixes it never disagree, phrased for a read-only report.
func statusAdvice(plan link.Plan) string {
	return "Safe next action: " + blockedReason(plan)
}

func statusView(plan link.Plan) stackView {
	view, native := membershipView(plan, "status")
	if len(plan.Issues) != 0 {
		return structureNote(view.block(statusAdvice(plan)), plan.Snapshot)
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
		return fmt.Sprintf("GitHub stack #%d · partial (%d/%d linked) · run g2g link to add the marked PRs.", s.StackNumber, s.Linked, s.Selected)
	case githubstack.Conflicting:
		return "GitHub stack: conflicting membership · review the marked PRs before changing anything."
	default:
		return "GitHub stack: not linked · run g2g link to preview a link."
	}
}

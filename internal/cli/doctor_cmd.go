package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/push"
	"github.com/shhac/g2g/internal/restack"
)

// newDoctor is status narrowed to what went wrong.
//
// status draws the stack you are on and everything about it; doctor reads every
// recorded stack and says only what is not as it should be, each with the
// command that puts it right. Most of what it finds broke outside this tool —
// a branch deleted with plain git, a parent rebased by hand, a force push from
// somewhere else — which is why it is a command of its own rather than a mode:
// it is what to run when something feels off, and its exit status answers
// whether anything is.
func newDoctor(service graph.Service, restacker restack.Service, published Published, presentation Presentation) *cobra.Command {
	var remote string
	cmd := &cobra.Command{
		Use:     "doctor",
		GroupID: groupLook,
		Short:   "Find what needs putting right, across every recorded stack (read-only, offline)",
		Long: "Reads every recorded stack and reports only what is not as it should be, each with the command " +
			"that puts it right: a branch whose parent moved, one deleted or renamed with plain git, a parent " +
			"that is gone, work that has already landed, a restack that stopped part-way, a branch that has " +
			"diverged from its remote.\n\n" +
			"It asks nothing of the network. It exits 0 when it finds nothing, and 1 when it finds something.",
		Args: cobra.NoArgs,
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		presentation := presentation.resolve(cmd)
		ctx, cancel := newBudgets(cmd).discovery(commandContext(cmd.Context(), cmd, "doctor", "read_only", "", ""))
		defer cancel()
		from, err := doctorStart(ctx, service)
		if err != nil {
			return err
		}
		discovery, err := service.Discover(ctx, graph.Selection{Branch: from, Scope: graph.ScopeAll})
		if err != nil {
			return err
		}
		interrupted, err := restacker.InProgress(ctx)
		if err != nil {
			return err
		}
		publishing, err := readPublished(ctx, published, remote, cmd.Flags().Changed("remote"), discovery)
		if err != nil {
			return err
		}
		findings := diagnose(discovery, interrupted, publishing, remote)
		if err := writeStackView(cmd.OutOrStdout(), doctorView(discovery, findings), presentation); err != nil {
			return err
		}
		if len(findings) != 0 {
			return foundProblems(len(findings))
		}
		return nil
	}
	cmd.Flags().StringVar(&remote, "remote", "origin", "the remote whose last-known branches each one is compared with")
	return cmd
}

// doctorStart is the branch a whole-repository read is anchored on. Every
// scope is chosen relative to one, and "all" answers the same from any of them
// — but a detached HEAD, which is where someone mid-rebase stands, has none,
// and that is exactly when doctor is wanted. A recorded root answers instead.
func doctorStart(ctx context.Context, service graph.Service) (string, error) {
	if current, err := service.Git.CurrentBranch(ctx); err == nil {
		return current, nil
	}
	recorded, err := service.Store.Load(ctx)
	if err != nil {
		return "", err
	}
	roots := recorded.Roots()
	if len(roots) == 0 {
		return "", errors.New("HEAD is detached and nothing is recorded, so there is nothing to examine")
	}
	return roots[0], nil
}

// finding is one thing that is not as it should be, and the way to put it
// right. Branch is empty for one that is about the repository as a whole.
type finding struct {
	Branch   string
	Problem  string
	Command  string
	Severity severity
}

// diagnose names every finding, in the order the stacks are drawn.
//
// A branch with no commits of its own is not one: it is as likely to be a
// branch nobody has started as one that is finished, and saying either would
// be a guess.
func diagnose(discovery graph.Discovery, interrupted bool, publishing map[string]push.Publication, remote string) []finding {
	findings := make([]finding, 0)
	if interrupted {
		findings = append(findings, finding{
			Problem:  "a restack stopped part-way",
			Command:  "g2g restack --continue",
			Severity: severityBad,
		})
	}
	orphans := make(map[string]bool)
	for _, branch := range discovery.Orphans() {
		orphans[branch] = true
	}
	for _, branch := range discovery.Branches {
		if found, problem := branchFinding(discovery, branch); problem {
			findings = append(findings, found)
			continue
		}
		if orphans[branch] {
			findings = append(findings, finding{Branch: branch, Problem: "no tracked parent", Command: "g2g track --branch " + branch, Severity: severityWarn})
			continue
		}
		publication, compared := publishing[branch]
		if compared && !discovery.Graph.IsTrunk(branch) && publication.Theirs > 0 && publication.Ours > 0 {
			findings = append(findings, finding{
				Branch:   branch,
				Problem:  fmt.Sprintf("diverged from %s · %d here, %d there", remote, publication.Ours, publication.Theirs),
				Command:  "g2g pull --branch " + branch,
				Severity: severityBad,
			})
		}
	}
	return findings
}

// branchFinding is what the graph's own state says is wrong with a branch.
func branchFinding(discovery graph.Discovery, branch string) (finding, bool) {
	parent, _ := discovery.Graph.Parent(branch)
	switch discovery.States[branch] {
	case graph.StateNeedsRestack:
		return finding{Branch: branch, Problem: "its parent moved underneath it", Command: "g2g restack --branch " + branch, Severity: severityWarn}, true
	case graph.StateMovedOffParent:
		return finding{Branch: branch, Problem: "no longer built on " + parent, Command: "g2g track --branch " + branch, Severity: severityWarn}, true
	case graph.StateForkUnresolvable:
		return finding{Branch: branch, Problem: "its recorded fork point is gone", Command: "g2g track --branch " + branch + " --parent " + parent, Severity: severityBad}, true
	case graph.StateParentMissing:
		return finding{Branch: branch, Problem: parent + " is no longer a local branch", Command: "g2g track --branch " + branch, Severity: severityWarn}, true
	case graph.StateBranchMissing:
		return finding{Branch: branch, Problem: "recorded, and no longer a local branch", Command: "g2g untrack --branch " + branch, Severity: severityWarn}, true
	case graph.StateLanded:
		return finding{Branch: branch, Problem: "already landed in " + parent, Command: "g2g prune --branch " + branch, Severity: severityNeutral}, true
	default:
		return finding{}, false
	}
}

// doctorView lists the findings and nothing else: the branches they are about,
// each with what is wrong, and the command for each beneath.
func doctorView(discovery graph.Discovery, findings []finding) stackView {
	view := stackView{Operation: "doctor", Target: "every recorded stack", TargetSource: "repository"}
	for _, found := range findings {
		if found.Branch != "" {
			view.Nodes = append(view.Nodes, stackNode{Branch: found.Branch, State: found.Problem, Severity: found.Severity})
		}
	}
	for _, found := range findings {
		subject := found.Problem
		if found.Branch != "" {
			subject = found.Branch + ": " + found.Problem
		}
		view = view.note(subject+" · run "+runnable(found.Command)+".", found.Severity)
	}
	if len(findings) == 0 {
		return view.note(fmt.Sprintf("Nothing needs putting right across %s.", count(len(discovery.Graph.Edges), "recorded branch", "recorded branches")), severityOK)
	}
	return view
}

// foundError is a doctor that found something. The report is already on
// stdout, so like a stop part-way it adds nothing on stderr; it has its own
// exit status because a script asking "is anything wrong" wants the answer and
// not an error.
type foundError struct{ count int }

func (e foundError) Error() string {
	return fmt.Sprintf("found %s", count(e.count, "problem", "problems"))
}

func foundProblems(count int) error { return foundError{count} }

func foundSomething(err error) bool {
	var found foundError
	return errors.As(err, &found)
}

// foundExitCode is doctor's answer that something needs putting right: 0
// healthy, 1 found, 2 could not tell — the convention diff and grep use.
const foundExitCode = 1

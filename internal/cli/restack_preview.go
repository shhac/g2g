package cli

import (
	"fmt"
	"slices"
	"strings"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/restack"
	"github.com/shhac/g2g/internal/syncpoint"
	"github.com/shhac/g2g/internal/words"
)

func restackView(plan restack.Plan) stackView {
	view := graphView(plan.Discovery, "restack")
	if plan.Blocked() != "" {
		return view.refusing(plan.Repair)
	}
	view = worktreeNotes(view, plan.Worktrees, plan.Policy, "g2g restack", "")
	if plan.Nothing() {
		// Branches left in other worktrees are not sitting on their parents,
		// and the notes above already say where they are.
		if len(plan.Worktrees.Leave) != 0 {
			return view
		}
		return view.note("Every selected branch already sits on its parent. Nothing to replay.", severityOK)
	}
	if len(plan.Steps) == 0 {
		return view.note("Already sits on "+plan.Onto.Parent+" · nothing to replay, only the new parent to record.", severityOK)
	}
	// A branch that collapses is not replayed, only moved, and saying it was
	// replayed would misdescribe what happened to its commits.
	if replaying := plan.Replaying(); len(replaying) != 0 {
		view = view.note("Replays "+ontoEach(plan, replaying)+".", severityOK)
	} else {
		view = view.note("Moves "+ontoEach(plan, plan.Emptied())+" · nothing needs replaying.", severityOK)
	}
	view = orphanNote(view, plan)
	view = emptiedNote(view, plan)
	view = strandedNote(view, plan)
	view = behindNote(view, plan)
	return engineNote(view, plan)
}

// behindNote warns that the replay is onto a trunk its remote, as last
// fetched, has moved on from. A parent squash-merged upstream is in the
// remote's trunk and not this one, so the replay keeps the parent's original
// commits and the pull request conflicts with the squash.
func behindNote(view stackView, plan restack.Plan) stackView {
	for _, trunk := range plan.Behind {
		remote := localgit.DefaultRemote + "/" + trunk.Trunk
		pull := selectedIn(plan.Discovery).next(pullCommand)
		onto := repair.Command([]string{"g2g", "restack", "--onto", remote})
		if plan.Discovery.Target != "" {
			onto = repair.Command([]string{"g2g", "restack", "--branch", plan.Discovery.Target, "--onto", remote})
		}
		view = view.note(fmt.Sprintf("%s is %s behind %s as last fetched, and this replays onto %s as it is here · %s brings it up to date and replays onto that instead, or %s if %s is held with changes in another worktree.",
			trunk.Trunk, words.Count(trunk.Commits, "commit", "commits"), remote, trunk.Trunk, runnable(pull), runnable(onto), trunk.Trunk), severityWarn)
	}
	return view
}

// strandedNote names the branches outside the selection left on the commits
// a moving parent had, and the one restack that brings each along.
func strandedNote(view stackView, plan restack.Plan) stackView {
	for _, root := range strandedRoots(plan) {
		branches, parents := []string{}, []string{}
		for _, left := range plan.Stranded {
			if left.Root != root {
				continue
			}
			branches = append(branches, left.Branch)
			if !slices.Contains(parents, left.Parent) {
				parents = append(parents, left.Parent)
			}
		}
		view = view.note("Leaves "+words.List(branches)+", outside this selection, on the commits "+words.List(parents)+" had before this replay · "+runnable(strandedCommand(root))+" brings "+words.Pick(len(branches), "it", "them")+" along once this is applied.", severityWarn)
	}
	return view
}

// strandedRoots is each rewrite root something was left on, in order.
func strandedRoots(plan restack.Plan) []string {
	roots := []string{}
	for _, left := range plan.Stranded {
		if !slices.Contains(roots, left.Root) {
			roots = append(roots, left.Root)
		}
	}
	return roots
}

// strandedCommand is the restack that covers a root and everything on it.
func strandedCommand(root string) string {
	return repair.Command([]string{"g2g", "restack", "--branch", root, "--scope", "subtree"})
}

// ontoEach says where each branch lands, grouped by the root it replays with.
// A selection can have several roots on different parents, and naming the
// first one's parent for all of them told the reader the wrong destination.
// Each root is named by the parent it has once the plan has run, which for
// pull is the trunk rather than the ref it fetched the trunk into.
func ontoEach(plan restack.Plan, branches []string) string {
	parent := map[string]string{}
	collapses := map[string]bool{}
	for _, step := range plan.Steps {
		parent[step.Branch] = step.Parent
		collapses[step.Branch] = step.Collapses
	}
	rootParent := func(branch string) string {
		if collapses[branch] {
			return plan.ParentAfter(branch)
		}
		for {
			above, stepped := parent[branch]
			if _, rewritten := parent[above]; !stepped || !rewritten {
				return plan.ParentAfter(branch)
			}
			branch = above
		}
	}
	order := make([]string, 0)
	grouped := map[string][]string{}
	for _, branch := range branches {
		onto := rootParent(branch)
		if _, seen := grouped[onto]; !seen {
			order = append(order, onto)
		}
		grouped[onto] = append(grouped[onto], branch)
	}
	said := make([]string, 0, len(order))
	for _, onto := range order {
		said = append(said, words.List(grouped[onto])+" onto "+replayedOnto(plan, onto))
	}
	return strings.Join(said, "; ")
}

// replayedOnto names where the commits land when that is a remote's copy of
// the parent the graph will record, rather than the parent itself.
func replayedOnto(plan restack.Plan, parent string) string {
	if object := plan.Onto.Object; parent == plan.Onto.Parent && object != "" && object != parent {
		return object + ", recorded under " + parent
	}
	return parent
}

// orphanNote names commits a rewritten parent dropped that a child still
// carries. Silently changing what a branch contains is the one thing this
// command must never do.
func orphanNote(view stackView, plan restack.Plan) stackView {
	orphans := plan.Orphaned()
	if len(orphans) == 0 {
		return view
	}
	dropped, listed := words.Count(len(orphans), "commit", "commits"), orphanList(plan)
	if plan.Absorb {
		return view.note("Keeps "+dropped+" the parent dropped, by re-recording where the branch forks"+listed+". Nothing is rewritten.", severityWarn)
	}
	note := "The parent dropped " + dropped + " this branch still carries; they will be dropped here too" + listed + "."
	if plan.Absorbable() {
		return view.note(note+" Use --absorb to keep them instead.", severityWarn)
	}
	// A rewritten commit still exists in the parent under a new object id, so
	// keeping the old copy would duplicate it.
	return view.note(note+" They cannot be absorbed: the parent rewrote rather than removed them.", severityWarn)
}

// orphanList names each orphan by the branch still carrying it, its short id
// and its subject: a commit about to leave a branch is listed, never counted,
// so it can be found again with git branch <name> <commit> if that was wrong.
func orphanList(plan restack.Plan) string {
	orphans := make([]syncpoint.Drop, 0)
	for _, step := range plan.Steps {
		for _, orphan := range step.Orphans {
			orphans = append(orphans, syncpoint.Drop{Branch: step.Branch, Commit: orphan})
		}
	}
	if len(orphans) == 0 {
		return ""
	}
	return ": " + dropList(plan.Subjects, orphans)
}

// emptiedNote names the command the apply will suggest next. It used to say
// untrack, which strands whatever sits on the branch; prune forgets it and
// records what sat on it where the replay left it.
func emptiedNote(view stackView, plan restack.Plan) stackView {
	emptied := plan.Emptied()
	if len(emptied) == 0 {
		return view
	}
	own := words.Pick(len(emptied), "its", "their")
	prune := replayNext(plan, "")
	return view.note("Leaves "+words.List(emptied)+" with no commits of "+own+" own, since "+own+" work is already in "+own+" new base · "+runnable(prune)+" forgets "+words.Pick(len(emptied), "it", "them")+" once this is applied.", severityNeutral)
}

// engineNote is the informed-consent line. A rewrite that cannot apply cleanly
// has to run in the user's working tree, and they should know that before they
// ask for it rather than afterwards.
func engineNote(view stackView, plan restack.Plan) stackView {
	if plan.Absorb {
		return view
	}
	if !plan.Predicted {
		// Saying it will conflict would be a claim we have not made, and the
		// reason there is no preview is worth saying: an old Git and a parent
		// that only exists once sync has brought it down want different
		// responses.
		reason := plan.Unpredicted
		if reason == "" {
			reason = "the result cannot be previewed"
		}
		return view.note(strings.ToUpper(reason[:1])+reason[1:]+", so applying rebases in your working tree. If it stops on a conflict, resolve it and run "+runnable("g2g restack --continue")+".", severityWarn)
	}
	if plan.Clean && len(plan.Worktrees.Follow) != 0 {
		return view.note("Applies without touching your working tree or checked-out branch; only the worktrees named above are brought along.", severityNeutral)
	}
	if plan.Clean {
		return view.note("Applies without touching your working tree or checked-out branch.", severityNeutral)
	}
	return view.note("This will not apply cleanly. Applying rebases in your working tree and stops on the conflict for you to resolve, then "+runnable("g2g restack --continue")+".", severityWarn)
}

package restack

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/graph"
)

func TestRecoveryRefusesAnotherWorktreeBeforeTouchingAnything(t *testing.T) {
	for _, verb := range []string{"continue", "skip", "abort"} {
		t.Run(verb, func(t *testing.T) {
			r := conflictingStack(t)
			ctx := context.Background()
			r.stopOnConflict(graph.Selection{Branch: "synthetic-b", Scope: graph.ScopeStack})
			before, _, err := r.service.Journal.Load(ctx)
			if err != nil {
				t.Fatal(err)
			}
			other := filepath.Join(t.TempDir(), "synthetic-other")
			r.Run("worktree", "add", "-q", "--detach", other, "synthetic-main")
			t.Chdir(other)
			methods := map[string]func(context.Context) error{"continue": r.service.Continue, "skip": r.service.Skip, "abort": r.service.Abort}
			if err := methods[verb](ctx); err == nil || !strings.Contains(err.Error(), "no restack is in progress in this worktree") || !strings.Contains(err.Error(), "unfinished in checkout "+strconv.Quote(resolved(t, r.Dir))) {
				t.Fatalf("%s error = %v, want the refusal naming the checkout it belongs to", verb, err)
			}
			if status := r.Run("-C", other, "status", "--porcelain"); status != "" {
				t.Fatalf("other worktree changed: %s", status)
			}
			t.Chdir(r.Dir)
			after, found, err := r.service.Journal.Load(ctx)
			if err != nil || !found || after.Worktree != before.Worktree {
				t.Fatalf("journal lost or changed: %+v, %v, %v", after, found, err)
			}
			if active, err := r.client.RebaseInProgress(ctx); err != nil || !active {
				t.Fatalf("original rebase changed: %v, %v", active, err)
			}
			if err := r.service.Abort(ctx); err != nil {
				t.Fatal(err)
			}
			r.assertClean()
		})
	}
}

func TestAbortRefusesACompletedBranchHeldElsewhere(t *testing.T) {
	r := conflictingStack(t)
	ctx := context.Background()
	r.stopOnConflict(graph.Selection{Branch: "synthetic-b", Scope: graph.ScopeStack})
	r.Write("a.txt", "synthetic resolved")
	r.Run("add", "a.txt")
	if err := r.service.Continue(ctx); err == nil {
		t.Fatal("expected synthetic-b conflict")
	}
	completed := r.Revision("synthetic-a")
	other := filepath.Join(t.TempDir(), "synthetic-other")
	r.Run("worktree", "add", "-q", other, "synthetic-a")
	if err := r.service.Abort(ctx); err == nil || !strings.Contains(err.Error(), "another worktree") {
		t.Fatalf("Abort error = %v, want held-branch refusal", err)
	}
	if got := r.Revision("synthetic-a"); got != completed {
		t.Fatal("abort partially restored synthetic-a")
	}
	if active, _ := r.client.RebaseInProgress(ctx); !active {
		t.Fatal("abort ended the active rebase before refusing")
	}
	if active, _ := r.service.InProgress(ctx); !active {
		t.Fatal("abort cleared its journal")
	}
	r.Run("-C", other, "switch", "-q", "--detach")
	if status := r.Run("-C", other, "status", "--porcelain"); status != "" {
		t.Fatalf("other worktree stranded: %s", status)
	}
	if err := r.service.Abort(ctx); err != nil {
		t.Fatal(err)
	}
	r.assertClean()
	if status := r.Run("-C", other, "status", "--porcelain"); status != "" {
		t.Fatalf("other worktree changed: %s", status)
	}
}

// Model interruption at the actual boundary: replay has moved the refs, but
// the checkout and graph have not been reconciled. Also retry after the index
// has already caught up, as a crash can occur immediately after read-tree.
func TestRecoveryReconcilesAnInterruptedBareRefMove(t *testing.T) {
	for _, verb := range []string{"continue", "abort"} {
		for _, resettled := range []bool{false, true} {
			t.Run(verb+map[bool]string{false: "/stale-index", true: "/updated-index"}[resettled], func(t *testing.T) {
				r := newRealStack(t)
				ctx := context.Background()
				if supported, err := r.client.SupportsReplay(ctx); err != nil {
					t.Fatal(err)
				} else if !supported {
					t.Skip("this Git is below the verified replay baseline")
				}
				r.branch("synthetic-a", "synthetic-main", "a.txt", "synthetic a")
				original := r.Revision("synthetic-a")
				r.Run("switch", "-q", "synthetic-main")
				r.Commit("synthetic advance", "new.txt", "synthetic new")
				r.Run("switch", "-q", "synthetic-a")
				plan := r.plan(graph.Selection{Branch: "synthetic-a", Scope: graph.ScopeStack})
				standing, err := r.service.standingOn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := r.service.begin(ctx, plan, standing); err != nil {
					t.Fatal(err)
				}
				if err := r.service.replay(ctx, plan); err != nil {
					t.Fatal(err)
				}
				if resettled {
					if err := r.service.resettle(ctx, standing); err != nil {
						t.Fatal(err)
					}
				}
				// Unrelated work must survive recovery, even though HEAD moved.
				r.Write("root.txt", "synthetic local edit")
				method := r.service.Continue
				if verb == "abort" {
					method = r.service.Abort
				}
				if err := method(ctx); err != nil {
					t.Fatal(err)
				}
				if verb == "abort" && r.Revision("synthetic-a") != original {
					t.Fatal("abort did not restore original tip")
				}
				if verb == "continue" {
					r.builtOn("synthetic-main", "synthetic-a")
				}
				if got := r.Run("diff", "--name-only"); got != "root.txt" {
					t.Fatalf("local edit lost or phantom changes: %s", got)
				}
				if staged := r.Run("diff", "--cached", "--name-only"); staged != "" {
					t.Fatalf("phantom staged changes: %s", staged)
				}
				if active, _ := r.service.InProgress(ctx); active {
					t.Fatal("finished operation kept journal")
				}
			})
		}
	}
}

func TestSkipAfterASecondConflictFinishesTheDescendant(t *testing.T) {
	r := newRealStack(t)
	ctx := context.Background()
	r.Commit("synthetic first base", "first.txt", "synthetic base")
	r.Commit("synthetic second base", "second.txt", "synthetic base")
	r.branch("synthetic-a", "synthetic-main", "first.txt", "synthetic first change")
	r.Commit("synthetic second change", "second.txt", "synthetic second change")
	r.branch("synthetic-b", "synthetic-a", "b.txt", "synthetic child")
	r.Run("switch", "-q", "synthetic-main")
	r.Write("first.txt", "synthetic upstream first")
	r.Write("second.txt", "synthetic upstream second")
	r.Run("commit", "-qam", "synthetic upstream changes")
	r.Run("switch", "-q", "synthetic-b")
	r.stopOnConflict(graph.Selection{Branch: "synthetic-b", Scope: graph.ScopeStack})
	r.Write("first.txt", "synthetic resolved first")
	r.Run("add", "first.txt")
	if err := r.service.Continue(ctx); err == nil {
		t.Fatal("second commit did not stop on its own conflict")
	}
	if paths, err := r.service.Conflicted(ctx); err != nil || strings.Join(paths, ",") != "second.txt" {
		t.Fatalf("second conflict = %v, %v", paths, err)
	}
	if err := r.service.Skip(ctx); err != nil {
		t.Fatal(err)
	}
	r.builtOn("synthetic-main", "synthetic-a")
	r.builtOn("synthetic-a", "synthetic-b")
	if got := r.Run("show", "synthetic-b:second.txt"); got != "synthetic upstream second" {
		t.Fatalf("skipped change survived: %s", got)
	}
	if got := r.Run("branch", "--show-current"); got != "synthetic-b" {
		t.Fatalf("did not return to original branch: %s", got)
	}
	r.assertClean()
}

func TestAnUnrelatedStackRestacksBesideAnotherWorktreesUnfinishedOne(t *testing.T) {
	r := conflictingStack(t)
	ctx := context.Background()
	r.branch("synthetic-x", "synthetic-main", "x.txt", "synthetic x")
	r.amend("synthetic-main", "root.txt", "synthetic amended root")
	r.stopOnConflict(graph.Selection{Branch: "synthetic-b", Scope: graph.ScopeStack})
	before, _, err := r.service.Journal.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "synthetic-other")
	r.Run("worktree", "add", "-q", other, "synthetic-x")
	t.Chdir(other)
	plan := r.plan(graph.Selection{Branch: "synthetic-x", Scope: graph.ScopeStack})
	if len(plan.Steps) == 0 {
		t.Fatal("unrelated stack has nothing to replay")
	}
	if err := r.service.Apply(ctx, plan); err != nil {
		t.Fatalf("an unrelated stack was refused beside another worktree's restack: %v", err)
	}
	if r.Try("merge-base", "--is-ancestor", "synthetic-main", "synthetic-x") != nil {
		t.Fatal("synthetic-x was not replayed onto the amended trunk")
	}
	if status := r.Run("-C", other, "status", "--porcelain"); status != "" {
		t.Fatalf("other worktree changed: %s", status)
	}
	// What the other restack moves is still refused from here.
	if err := r.service.RequireApart(ctx, Reads("synthetic-b")); err == nil || !strings.Contains(err.Error(), "unfinished in checkout") {
		t.Fatalf("restacking what the other restack moves = %v, want it refused", err)
	}
	t.Chdir(r.Dir)
	after, found, err := r.service.Journal.Load(ctx)
	if err != nil || !found || after.Branch != before.Branch || after.Worktree != before.Worktree {
		t.Fatalf("existing journal replaced: %+v, %v, %v", after, found, err)
	}
	if active, _ := r.client.RebaseInProgress(ctx); !active {
		t.Fatal("original rebase lost")
	}
	if err := r.service.Abort(ctx); err != nil {
		t.Fatal(err)
	}
	r.assertClean()
}

// A collapse is the other bare-ref engine and needs no git replay support.
func TestRecoveryReconcilesAnInterruptedCollapse(t *testing.T) {
	for _, verb := range []string{"continue", "abort"} {
		t.Run(verb, func(t *testing.T) {
			r := newRealStack(t)
			ctx := context.Background()
			r.branch("synthetic-a", "synthetic-main", "a.txt", "synthetic a")
			original := r.Revision("synthetic-a")
			r.Run("switch", "-q", "synthetic-main")
			r.Run("merge", "-q", "--squash", "synthetic-a")
			r.Run("commit", "-qm", "synthetic squash")
			r.Commit("synthetic advance", "new.txt", "synthetic new")
			r.Run("switch", "-q", "synthetic-a")
			plan := r.plan(graph.Selection{Branch: "synthetic-a", Scope: graph.ScopeStack})
			if len(plan.collapsing()) != 1 {
				t.Fatal("expected a collapse")
			}
			standing, err := r.service.standingOn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := r.service.begin(ctx, plan, standing); err != nil {
				t.Fatal(err)
			}
			if err := r.service.collapse(ctx, plan); err != nil {
				t.Fatal(err)
			}
			method := r.service.Continue
			want := r.Revision("synthetic-main")
			if verb == "abort" {
				method, want = r.service.Abort, original
			}
			if err := method(ctx); err != nil {
				t.Fatal(err)
			}
			if got := r.Revision("synthetic-a"); got != want {
				t.Fatalf("tip = %s, want %s", got, want)
			}
			r.assertClean()
		})
	}
}

// A branch deleted with plain Git between a stop and --continue failed with
// Git's own error about an object it could not name, because recording the
// structure asks about every selected branch. untrack forgets such an edge
// anywhere else, but it refuses while the journal exists, so the refusal has
// to name abort — and abort has to actually bring the branch back.
func TestContinueAfterADeletedBranchNamesAbortWhichRestoresIt(t *testing.T) {
	r := conflictingStack(t)
	ctx := context.Background()
	original := r.Revision("synthetic-b")
	r.stopOnConflict(graph.Selection{Branch: "synthetic-b", Scope: graph.ScopeStack})
	r.Run("rebase", "--abort")
	r.Run("switch", "-q", "synthetic-main")
	r.Run("branch", "-D", "synthetic-b")

	err := r.service.Continue(ctx)
	if err == nil || !strings.Contains(err.Error(), "synthetic-b is recorded but is no longer a local branch · run g2g restack --abort") {
		t.Fatalf("Continue error = %v, want the deleted branch named with abort as the way out", err)
	}
	if active, _ := r.service.InProgress(ctx); !active {
		t.Fatal("the refusal cleared the journal, so the advised abort has nothing to restore")
	}
	if err := r.service.Abort(ctx); err != nil {
		t.Fatal(err)
	}
	if got := r.Revision("synthetic-b"); got != original {
		t.Fatalf("abort left synthetic-b at %s, want its original tip %s", got, original)
	}
	r.assertClean()
}

// A user who finishes git's own rebase by hand is left standing on the
// rewritten branch with no rebase in progress. Abort then moved the refs back
// and nothing else, so the working tree still held the rewrite and git status
// reported it as staged changes — under "Every branch is back where it
// started".
func TestAbortAfterAHandFinishedRebaseBringsTheCheckoutBack(t *testing.T) {
	r := conflictingStack(t)
	r.Run("switch", "-q", "synthetic-b")
	before := map[string]string{"synthetic-a": r.Revision("synthetic-a"), "synthetic-b": r.Revision("synthetic-b")}
	r.stopOnConflict(graph.Selection{Branch: "synthetic-b", Scope: graph.ScopeStack})

	r.Write("a.txt", "resolved")
	r.Run("add", "a.txt")
	r.Run("-c", "core.editor=true", "rebase", "--continue")

	if err := r.service.Abort(context.Background()); err != nil {
		t.Fatalf("Abort() error = %v", err)
	}
	for branch, tip := range before {
		if got := r.Revision(branch); got != tip {
			t.Errorf("%s = %s after abort, want %s", branch, got, tip)
		}
	}
	r.assertClean()
}

// A resumed pass records fork points as it goes, so it can plan against the
// work it has already done. Abort put the tips back and left those, so every
// restored branch was recorded as forking at a parent tip it did not contain,
// and the next restack refused the whole stack as moved off its parent.
func TestAbortPutsBackTheStructureAResumeRecorded(t *testing.T) {
	r := conflictingStack(t)
	ctx := context.Background()
	before, err := r.store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	selection := graph.Selection{Branch: "synthetic-b", Scope: graph.ScopeStack}
	r.stopOnConflict(selection)
	r.Write("a.txt", "resolved")
	r.Run("add", "a.txt")
	// synthetic-a is finished and recorded; synthetic-b stops on its own
	// conflict.
	if err := r.service.Continue(ctx); err == nil {
		t.Fatal("Continue() error = nil; synthetic-b was meant to conflict too")
	}

	if err := r.service.Abort(ctx); err != nil {
		t.Fatalf("Abort() error = %v", err)
	}

	after, err := r.store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, branch := range []string{"synthetic-a", "synthetic-b"} {
		if got, want := after.Edges[branch], before.Edges[branch]; got != want {
			t.Errorf("%s is recorded as %+v after abort, want %+v", branch, got, want)
		}
		pinned := r.Run("rev-parse", "refs/g2g/forkpoints/"+branch)
		if pinned != before.Edges[branch].ForkPoint {
			t.Errorf("%s fork point pinned at %s after abort, want %s", branch, pinned, before.Edges[branch].ForkPoint)
		}
	}
	// The stack is back where it was, so it plans exactly as it did before.
	if replaying := strings.Join(r.plan(selection).Replaying(), ","); replaying != "synthetic-a,synthetic-b" {
		t.Errorf("after abort the plan replays %q, want the whole stack again", replaying)
	}
}

// Resuming recomputes the plan, and the recomputed plan can refuse: here a
// branch still to be rewritten was opened in another worktree while the first
// conflict was being resolved. That refusal was read as completion, so the
// command said "Restack complete" and deleted the journal over a stack that
// was half rewritten, leaving nothing for --abort.
func TestAResumeThatIsRefusedKeepsTheJournal(t *testing.T) {
	r := conflictingStack(t)
	ctx := context.Background()
	r.stopOnConflict(graph.Selection{Branch: "synthetic-b", Scope: graph.ScopeStack})
	untouched := r.Revision("synthetic-b")
	r.Run("worktree", "add", "-q", filepath.Join(t.TempDir(), "elsewhere"), "synthetic-b")
	r.Write("a.txt", "resolved")
	r.Run("add", "a.txt")

	err := r.service.Continue(ctx)
	if err == nil || !strings.Contains(err.Error(), "another worktree") {
		t.Fatalf("Continue() error = %v, want the refusal", err)
	}
	if inProgress, _ := r.service.InProgress(ctx); !inProgress {
		t.Error("a refused resume deleted the journal, so --abort has nothing to undo")
	}
	if got := r.Revision("synthetic-b"); got != untouched {
		t.Errorf("synthetic-b moved to %s while another worktree held it", got)
	}
}

// The same rule for a plan that refuses outright rather than one held by a
// worktree: here the branch still to be rewritten was pointed at unrelated
// history with plain Git while the first conflict was being resolved, so no
// range holds only its own commits. The rewrite of synthetic-a is done and
// synthetic-b's is not, so the stack is half rewritten and the journal is the
// only thing that can put it back.
func TestAResumeWhosePlanIsBlockedKeepsTheJournalForAbort(t *testing.T) {
	r := conflictingStack(t)
	ctx := context.Background()
	before := map[string]string{"synthetic-a": r.Revision("synthetic-a"), "synthetic-b": r.Revision("synthetic-b")}
	recorded, err := r.store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r.stopOnConflict(graph.Selection{Branch: "synthetic-b", Scope: graph.ScopeStack})
	unrelated := r.Run("commit-tree", "-m", "synthetic unrelated", r.Run("hash-object", "-t", "tree", "-w", "/dev/null"))
	r.Run("update-ref", "refs/heads/synthetic-b", unrelated)
	r.Write("a.txt", "resolved")
	r.Run("add", "a.txt")

	err = r.service.Continue(ctx)
	if err == nil || !strings.Contains(err.Error(), "cannot carry on") || !strings.Contains(err.Error(), "synthetic-b") {
		t.Fatalf("Continue() error = %v, want the refusal naming synthetic-b", err)
	}
	if inProgress, _ := r.service.InProgress(ctx); !inProgress {
		t.Fatal("a blocked resume deleted the journal, so --abort has nothing to undo")
	}

	if err := r.service.Abort(ctx); err != nil {
		t.Fatalf("Abort() error = %v", err)
	}
	for branch, tip := range before {
		if got := r.Revision(branch); got != tip {
			t.Errorf("%s = %s after abort, want %s", branch, got, tip)
		}
	}
	after, err := r.store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for branch, edge := range recorded.Edges {
		if after.Edges[branch] != edge {
			t.Errorf("%s is recorded as %+v after abort, want %+v", branch, after.Edges[branch], edge)
		}
	}
	r.assertClean()
}

// resolved is dir with its symbolic links resolved, as Git reports paths.
func resolved(t *testing.T, dir string) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// A restack paused part-way through replaying a child onto its parent is
// undone by another moving that parent, though neither moves what the other
// moves. So a restack of the parent from another worktree is refused, and
// moves nothing, while the paused one can still be finished.
func TestARestackOfTheParentAPausedOneReplaysOntoIsRefusedElsewhere(t *testing.T) {
	r := newRealStack(t)
	ctx := context.Background()
	r.Commit("synthetic shared", "c.txt", "base")
	r.branch("synthetic-parent", "synthetic-main", "p.txt", "synthetic parent")
	r.branch("synthetic-child", "synthetic-parent", "c.txt", "from the child")
	r.amend("synthetic-parent", "c.txt", "from the parent")
	r.Run("switch", "-q", "synthetic-child")
	r.stopOnConflict(graph.Selection{Branch: "synthetic-child", Scope: graph.ScopeBranch})

	other := filepath.Join(t.TempDir(), "synthetic-other")
	r.Run("worktree", "add", "-q", other, "synthetic-main")
	r.Run("-C", other, "commit", "-q", "--allow-empty", "-m", "synthetic trunk moves on")
	t.Chdir(other)
	before := r.Revision("synthetic-parent")
	plan := r.plan(graph.Selection{Branch: "synthetic-parent", Scope: graph.ScopeBranch})
	if len(plan.Steps) == 0 {
		t.Fatal("the parent has nothing to replay")
	}
	err := r.service.Apply(ctx, plan)
	if err == nil || !strings.Contains(err.Error(), "which holds synthetic-parent") {
		t.Fatalf("Apply error = %v, want it refused over the parent the paused restack replays onto", err)
	}
	if r.Revision("synthetic-parent") != before {
		t.Fatal("the refused restack moved the parent")
	}
	if found, _ := r.service.InProgress(ctx); found {
		t.Fatal("the refused restack left a journal of its own")
	}

	t.Chdir(r.Dir)
	if active, err := r.client.RebaseInProgress(ctx); err != nil || !active {
		t.Fatalf("the paused rebase changed: %v, %v", active, err)
	}
	if err := r.service.Abort(ctx); err != nil {
		t.Fatal(err)
	}
	r.assertClean()
}

// A restack an older g2g journaled in the shared file, for a linked worktree,
// stays that worktree's to finish or undo after the upgrade, and the main
// worktree, whose file it is in, cannot take it.
func TestALinkedWorktreeFinishesARestackJournaledInTheSharedFile(t *testing.T) {
	for _, verb := range []string{"abort", "continue"} {
		t.Run(verb, func(t *testing.T) {
			r := conflictingStack(t)
			ctx := context.Background()
			other := filepath.Join(t.TempDir(), "synthetic-other")
			r.Run("worktree", "add", "-q", other, "synthetic-b")
			t.Chdir(other)
			r.stopOnConflict(graph.Selection{Branch: "synthetic-b", Scope: graph.ScopeStack})
			common := strings.TrimSpace(r.Run("rev-parse", "--path-format=absolute", "--git-common-dir"))
			private := strings.TrimSpace(r.Run("-C", other, "rev-parse", "--absolute-git-dir"))
			own := filepath.Join(common, "g2g", "restack", "worktrees", filepath.Base(private)+".json")
			shared := filepath.Join(common, "g2g", "restack.json")
			if err := os.Rename(own, shared); err != nil {
				t.Fatal(err)
			}

			t.Chdir(r.Dir)
			if err := r.service.Abort(ctx); err == nil {
				t.Fatal("the main worktree aborted the linked worktree's restack")
			}
			t.Chdir(other)
			switch verb {
			case "abort":
				if err := r.service.Abort(ctx); err != nil {
					t.Fatalf("abort: %v", err)
				}
			case "continue":
				finished := false
				for range 4 {
					for _, file := range []string{"a.txt", "b.txt"} {
						if err := os.WriteFile(filepath.Join(other, file), []byte("synthetic resolved\n"), 0o600); err != nil {
							t.Fatal(err)
						}
						r.Run("-C", other, "add", file)
					}
					if err := r.service.Continue(ctx); err == nil {
						finished = true
						break
					}
				}
				if !finished {
					t.Fatal("continue never finished")
				}
			}
			for _, path := range []string{shared, own} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Errorf("%s left after %s: %v", path, verb, err)
				}
			}
			if active, _ := r.client.RebaseInProgress(ctx); active {
				t.Errorf("rebase still in progress after %s", verb)
			}
			if status := strings.TrimSpace(r.Run("-C", other, "status", "--porcelain")); status != "" {
				t.Errorf("the linked worktree is not clean after %s:\n%s", verb, status)
			}
		})
	}
}

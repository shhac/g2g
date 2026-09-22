package restack

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
)

// A fake answers whatever it is asked, and the questions here are what Git
// actually does: which commits a range reaches, what an engine produces, and
// whether the index follows a ref that moved. So these build a throwaway
// repository -- synthetic names, no remote, nothing that leaves the machine --
// and drive the service against it.

type realStack struct {
	testutil.GitRepo
	t       *testing.T
	client  localgit.Client
	store   graph.FileStore
	service Service
}

func newRealStack(t *testing.T) realStack {
	t.Helper()
	repo := testutil.NewGitRepo(t, "synthetic-main")
	// The code under test spawns its own git, which reads the developer's
	// global configuration, and a rebase records signing in its own state --
	// so a rebase it started could not be finished by a helper that reads none.
	repo.Run("config", "commit.gpgsign", "false")
	repo.Commit("synthetic root", "root.txt", "root")
	t.Chdir(repo.Dir)
	client := localgit.Client{Runner: subprocess.ExecRunner{}}
	store := graph.FileStore{Git: client}
	return realStack{
		GitRepo: repo,
		t:       t,
		client:  client,
		store:   store,
		service: Service{
			Git:     client,
			Graph:   graph.Service{Git: client, Store: store, Refs: client},
			Journal: FileJournal{Git: client},
		},
	}
}

// branch starts a branch from parent with one commit of its own, and records
// it where it forks.
func (r realStack) branch(name, parent, file, content string) {
	r.t.Helper()
	r.Run("switch", "-q", parent)
	r.Run("switch", "-qc", name)
	r.Commit("synthetic "+name, file, content)
	r.track(name, parent)
}

func (r realStack) track(name, parent string) {
	r.t.Helper()
	ctx := context.Background()
	adopted, err := r.store.Load(ctx)
	if err != nil {
		r.t.Fatal(err)
	}
	fork := r.Revision(parent)
	updated, _, err := adopted.Adopt(name, graph.Edge{Parent: parent, Origin: graph.OriginUser, ForkPoint: fork})
	if err != nil {
		r.t.Fatal(err)
	}
	if err := r.store.Save(ctx, updated); err != nil {
		r.t.Fatal(err)
	}
	if err := r.client.PinForkPoint(ctx, name, fork); err != nil {
		r.t.Fatal(err)
	}
}

// amend rewrites a branch's tip commit in place, which is what leaves the
// branches above it carrying the old one.
func (r realStack) amend(branch, file, content string) {
	r.t.Helper()
	r.Run("switch", "-q", branch)
	r.Write(file, content)
	r.Run("commit", "-qa", "--amend", "--no-edit")
}

func (r realStack) plan(selection graph.Selection) Plan {
	r.t.Helper()
	plan, err := r.service.Plan(context.Background(), selection, Onto{}, false, nil)
	if err != nil {
		r.t.Fatalf("Plan() error = %v", err)
	}
	if plan.Blocked != "" {
		r.t.Fatalf("Plan() blocked: %s", plan.Blocked)
	}
	return plan
}

// own counts the commits a branch carries above its parent.
func (r realStack) own(parent, branch string) int {
	r.t.Helper()
	return len(strings.Fields(r.Run("rev-list", parent+".."+branch)))
}

// builtOn reports that a branch sits on its parent's current tip with exactly
// one commit of its own, which is what every stack here should end up as.
func (r realStack) builtOn(parent, branch string) {
	r.t.Helper()
	if err := r.Try("merge-base", "--is-ancestor", parent, branch); err != nil {
		r.t.Errorf("%s is not built on %s", branch, parent)
	}
	if got := r.own(parent, branch); got != 1 {
		r.t.Errorf("%s carries %d commits above %s, want only its own", branch, got, parent)
	}
}

// assertClean is the check for the recurring bug: a ref moved and the index
// did not follow, which git reports as changes nobody made.
func (r realStack) assertClean() {
	r.t.Helper()
	if status := strings.TrimSpace(r.Run("status", "--porcelain")); status != "" {
		r.t.Errorf("working tree is not clean:\n%s", status)
	}
}

// Both stacks' roots are amended, so each has a child carrying the old commit.
// Replayed from one origin onto one base, the second stack's child landed on
// the first root with a stale copy of its own parent's commit, and the command
// then reported "Not applied" over refs it had already moved.
func TestIndependentRootsAreEachReplayedOntoTheirOwnParent(t *testing.T) {
	r := newRealStack(t)
	r.branch("synthetic-a", "synthetic-main", "a.txt", "a")
	r.branch("synthetic-b", "synthetic-a", "b.txt", "b")
	r.branch("synthetic-x", "synthetic-main", "x.txt", "x")
	r.branch("synthetic-y", "synthetic-x", "y.txt", "y")
	r.amend("synthetic-a", "a.txt", "a amended")
	r.amend("synthetic-x", "x.txt", "x amended")
	r.Run("switch", "-q", "synthetic-main")

	plan := r.plan(graph.Selection{Branch: "synthetic-b", Scope: graph.ScopeTrunk})
	if err := r.service.Apply(context.Background(), plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	r.builtOn("synthetic-a", "synthetic-b")
	r.builtOn("synthetic-x", "synthetic-y")
	r.assertClean()
}

// A squash-merged parent collapses onto the trunk, and the child above it
// conflicts. Standing on the parent, the collapse moved the checked-out
// branch, and the rebase that followed refused to start: git read the index
// that still described the old commit as uncommitted changes, left them
// staged, and left the journal behind with nothing to continue.
func TestACollapseOfTheCheckedOutBranchLetsTheRebaseStart(t *testing.T) {
	r := newRealStack(t)
	r.Commit("synthetic shared", "shared.txt", "base")
	r.branch("synthetic-a", "synthetic-main", "a.txt", "a")
	r.Commit("synthetic a again", "a2.txt", "a2")
	r.branch("synthetic-b", "synthetic-a", "shared.txt", "from b")
	r.Run("switch", "-q", "synthetic-main")
	r.Run("merge", "-q", "--squash", "synthetic-a")
	r.Run("commit", "-qm", "synthetic squash of a")
	r.Commit("synthetic trunk change", "shared.txt", "from the trunk")
	r.Run("switch", "-q", "synthetic-a")

	plan := r.plan(graph.Selection{Branch: "synthetic-b", Scope: graph.ScopeStack})
	if plan.Clean {
		t.Fatal("the plan predicts no conflict; this case needs the resumable engine")
	}
	if err := r.service.Apply(context.Background(), plan); err == nil {
		t.Fatal("Apply() error = nil; the rebase was meant to stop on the conflict")
	}

	conflicted, err := r.client.ConflictedPaths(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(conflicted, ",") != "shared.txt" {
		t.Fatalf("conflicted = %v, want the rebase stopped on shared.txt rather than refusing to start", conflicted)
	}

	r.Write("shared.txt", "resolved")
	r.Run("add", "shared.txt")
	if err := r.service.Continue(context.Background()); err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if got := r.Revision("synthetic-a"); got != r.Revision("synthetic-main") {
		t.Errorf("synthetic-a = %s, want it collapsed onto the trunk", got)
	}
	r.builtOn("synthetic-main", "synthetic-b")
	r.assertClean()
}

// conflictingStack is a two-branch stack whose rebase stops on its bottom
// branch, because the trunk changed the same lines both branches did.
func conflictingStack(t *testing.T) realStack {
	t.Helper()
	r := newRealStack(t)
	r.Commit("synthetic shared", "a.txt", "base")
	r.Commit("synthetic shared too", "b.txt", "base")
	r.branch("synthetic-a", "synthetic-main", "a.txt", "from a")
	r.branch("synthetic-b", "synthetic-a", "b.txt", "from b")
	r.Run("switch", "-q", "synthetic-main")
	r.Write("a.txt", "from the trunk")
	r.Write("b.txt", "from the trunk")
	r.Run("commit", "-qam", "synthetic trunk change")
	return r
}

// stopOnConflict applies a plan the preview says will conflict, and checks
// that it stopped rather than finishing or failing outright.
func (r realStack) stopOnConflict(selection graph.Selection) {
	r.t.Helper()
	plan := r.plan(selection)
	if err := r.service.Apply(context.Background(), plan); err == nil {
		r.t.Fatal("Apply() error = nil; the rebase was meant to stop on the conflict")
	}
	if inProgress, _ := r.service.InProgress(context.Background()); !inProgress {
		r.t.Fatal("the stopped rebase left no journal")
	}
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

// The rebase engine checks out what it rewrites, so a restack run from outside
// the stack ended on whichever branch was rebased last. The journal recorded a
// branch to return to, and nothing read it -- and what it recorded was the
// selection's target rather than where the user had been.
func TestARestackThroughTheRebaseEngineEndsWhereItStarted(t *testing.T) {
	for _, test := range []struct {
		name   string
		finish func(realStack) error
	}{
		{name: "continue", finish: func(r realStack) error {
			r.Write("a.txt", "resolved")
			r.Run("add", "a.txt")
			// Both branches conflict with the trunk, so resuming stops again.
			if err := r.service.Continue(context.Background()); err == nil {
				r.t.Fatal("Continue() error = nil; synthetic-b was meant to conflict too")
			}
			r.Write("b.txt", "resolved")
			r.Run("add", "b.txt")
			return r.service.Continue(context.Background())
		}},
		{name: "abort", finish: func(r realStack) error {
			return r.service.Abort(context.Background())
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := conflictingStack(t)
			r.stopOnConflict(graph.Selection{Branch: "synthetic-b", Scope: graph.ScopeStack})
			if err := test.finish(r); err != nil {
				t.Fatalf("%s error = %v", test.name, err)
			}
			if current := r.Run("branch", "--show-current"); current != "synthetic-main" {
				t.Errorf("checkout is on %q, want synthetic-main, where the restack was run from", current)
			}
			r.assertClean()
		})
	}
}

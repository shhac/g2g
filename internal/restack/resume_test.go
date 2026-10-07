package restack

import (
	"context"
	"errors"
	"strings"
	"testing"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/graph"
)

// stuckGit is a rebase engine that reports success and moves nothing, so every
// resumed pass plans exactly the work the last one claimed to have done.
type stuckGit struct{ *fakeGit }

func (g stuckGit) Rebase(_ context.Context, onto string, replayed localgit.Range) error {
	g.rebases = append(g.rebases, replayed)
	g.ontos = append(g.ontos, onto)
	return nil
}

// A resume that is not converging has to say so and stop. Each pass is meant
// to move at least one branch, so the loop has no other end: without the bound
// on passes, --continue spins forever re-running the same rebases.
func TestAResumeThatDoesNotSettleStopsAndKeepsTheJournal(t *testing.T) {
	git := stackGit()
	git.previewClean = false
	service, _, journal := newService(git, stack())
	service.Git = stuckGit{git}
	journal.record = Record{Branch: "synthetic-b", Scope: string(graph.ScopeTrunk), Original: map[string]string{"synthetic-a": "a-old", "synthetic-b": "b-old"}}
	journal.present = true

	err := service.Continue(context.Background())
	if err == nil || !strings.Contains(err.Error(), "did not settle") {
		t.Fatalf("Continue() error = %v, want the non-convergence refusal", err)
	}
	if !journal.present || journal.cleared != 0 {
		t.Error("a resume that did not settle cleared the journal, so --abort has nothing to undo")
	}
	if len(git.rebases) == 0 {
		t.Error("no pass ran; the case is meant to loop until the bound stops it")
	}
}

// flakyGit fails one recovery call once, at the point named, and then behaves.
type flakyGit struct {
	*fakeGit
	failUpdate      string
	failRebaseAbort bool
}

func (g *flakyGit) UpdateBranch(ctx context.Context, branch, object string) error {
	if branch == g.failUpdate {
		g.failUpdate = ""
		return errors.New("synthetic update-ref failure")
	}
	return g.fakeGit.UpdateBranch(ctx, branch, object)
}

func (g *flakyGit) RebaseAbort(ctx context.Context) error {
	if g.failRebaseAbort {
		g.failRebaseAbort = false
		return errors.New("synthetic rebase --abort failure")
	}
	return g.fakeGit.RebaseAbort(ctx)
}

// Abort is itself a run of separate Git calls, and one can fail after others
// have moved refs. The journal is the only record of where every branch began,
// so a failure part-way has to leave it in place: a second --abort then puts
// back every tip and every recorded edge, including the ones the first attempt
// had already restored.
func TestAnAbortThatFailsPartWayCanBeRetried(t *testing.T) {
	for _, test := range []struct {
		name  string
		flaky func(*fakeGit) *flakyGit
	}{
		{name: "restoring the second tip", flaky: func(git *fakeGit) *flakyGit {
			return &flakyGit{fakeGit: git, failUpdate: "synthetic-b"}
		}},
		{name: "aborting git's own rebase", flaky: func(git *fakeGit) *flakyGit {
			return &flakyGit{fakeGit: git, failRebaseAbort: true}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			git := stackGit()
			git.inProgress = true
			// Both branches were rewritten before the stop, and a resumed pass
			// recorded fork points to match.
			git.tips = map[string]string{"synthetic-a": "a-new", "synthetic-b": "b-new"}
			original := stack()
			resumed := stack()
			resumed.Edges["synthetic-a"] = graph.Edge{Parent: "synthetic-trunk", ForkPoint: "trunk-new"}
			resumed.Edges["synthetic-b"] = graph.Edge{Parent: "synthetic-a", ForkPoint: "a-new"}
			service, store, journal := newService(git, resumed)
			service.Git = test.flaky(git)
			journal.record = Record{
				Branch:   "synthetic-b",
				Scope:    string(graph.ScopeTrunk),
				Original: map[string]string{"synthetic-a": "a-old", "synthetic-b": "b-old"},
				Structure: map[string]RecordedEdge{
					"synthetic-a": {Parent: "synthetic-trunk", ForkPoint: "trunk-old"},
					"synthetic-b": {Parent: "synthetic-a", ForkPoint: "a-old"},
				},
			}
			journal.present = true
			ctx := context.Background()

			if err := service.Abort(ctx); err == nil {
				t.Fatal("Abort() error = nil; the first attempt was meant to fail part-way")
			}
			if inProgress, _ := service.InProgress(ctx); !inProgress {
				t.Fatal("a failed abort cleared the journal, so nothing can finish putting the stack back")
			}

			if err := service.Abort(ctx); err != nil {
				t.Fatalf("second Abort() error = %v", err)
			}
			for branch, want := range journal.record.Original {
				if got, _ := git.Resolve(ctx, branch); got != want {
					t.Errorf("%s = %s after the retried abort, want %s", branch, got, want)
				}
			}
			for branch, want := range original.Edges {
				if got := store.graph.Edges[branch]; got.Parent != want.Parent || got.ForkPoint != want.ForkPoint {
					t.Errorf("%s is recorded as %+v after the retried abort, want %+v", branch, got, want)
				}
			}
			if inProgress, _ := service.InProgress(ctx); inProgress {
				t.Error("the journal outlived the abort that finished")
			}
		})
	}
}

// Abort restores paths that already completed, which is the whole reason the
// journal records tips at all.
func TestAbortRestoresEveryRecordedTip(t *testing.T) {
	git := stackGit()
	git.inProgress = true
	service, _, journal := newService(git, stack())
	journal.record = Record{Original: map[string]string{"synthetic-a": "a-was", "synthetic-b": "b-was"}}
	journal.present = true

	if err := service.Abort(context.Background()); err != nil {
		t.Fatalf("Abort() error = %v", err)
	}

	if git.restored["synthetic-a"] != "a-was" || git.restored["synthetic-b"] != "b-was" {
		t.Errorf("restored = %v, want both original tips", git.restored)
	}
	if journal.cleared != 1 {
		t.Errorf("journal cleared %d times, want once", journal.cleared)
	}
}

// Continuing recomputes rather than replaying a stored queue, so a user who
// ran git rebase --continue themselves simply changes what work remains.
func TestContinueRecomputesRatherThanResumingAQueue(t *testing.T) {
	git := stackGit()
	// The user already finished the rebase by hand, so nothing is in progress
	// and every branch now sits where the graph says it should.
	git.objects["synthetic-trunk"] = "trunk-old"
	service, _, journal := newService(git, stack())
	journal.record = Record{Branch: "synthetic-b", Scope: string(graph.ScopeTrunk), Original: map[string]string{}}
	journal.present = true

	if err := service.Continue(context.Background()); err != nil {
		t.Fatalf("Continue() error = %v", err)
	}

	if len(git.steps) != 0 {
		t.Errorf("ran %v; nothing was in progress to continue", git.steps)
	}
	if journal.cleared != 1 {
		t.Error("the journal was not cleared once the work was done")
	}
}

// Finishing has to record fork points from the selection, not from the plan.
// Once the rewrite has succeeded the re-derived plan has no steps left, so
// recording only those records nothing and leaves every fork point describing
// the world before the rewrite.
func TestFinishingRecordsForkPointsWhenNoStepsRemain(t *testing.T) {
	git := stackGit()
	// Aligned already: the work happened, whether by us or by the user.
	git.objects["synthetic-trunk"] = "trunk-old"
	stale := stack()
	// The rewrite already happened, so the recorded fork point is not in the
	// branch's history any more. There is nothing left to replay, and the
	// stored structure is the only thing still out of date.
	stale.Edges["synthetic-b"] = graph.Edge{Parent: "synthetic-a", ForkPoint: "stale-fork"}
	git.objects["stale-fork"] = "stale-fork"
	service, store, journal := newService(git, stale)
	journal.record = Record{Branch: "synthetic-b", Scope: string(graph.ScopeTrunk), Original: map[string]string{}}
	journal.present = true

	if err := service.Continue(context.Background()); err != nil {
		t.Fatalf("Continue() error = %v", err)
	}

	if fork := store.graph.Edges["synthetic-b"].ForkPoint; fork != "a-old" {
		t.Errorf("fork point = %q, want the parent's current tip; it still describes the world before the rewrite", fork)
	}
	if git.pinned["synthetic-b"] == "" {
		t.Error("the refreshed fork point was not pinned")
	}
}

func TestContinueAndAbortRefuseWhenNothingIsInProgress(t *testing.T) {
	service, _, _ := newService(stackGit(), stack())

	if err := service.Continue(context.Background()); err == nil {
		t.Error("Continue() error = nil with no restack in progress")
	}
	if err := service.Abort(context.Background()); err == nil {
		t.Error("Abort() error = nil with no restack in progress")
	}
}

func TestSkipAdvancesTheInterruptedRewrite(t *testing.T) {
	git := stackGit()
	git.inProgress = true
	git.objects["synthetic-trunk"] = "trunk-old"
	service, _, journal := newService(git, stack())
	journal.record = Record{Branch: "synthetic-b", Scope: string(graph.ScopeTrunk), Original: map[string]string{}}
	journal.present = true

	if err := service.Skip(context.Background()); err != nil {
		t.Fatalf("Skip() error = %v", err)
	}
	if len(git.steps) != 1 || git.steps[0] != "skip" {
		t.Errorf("steps = %v, want one skip", git.steps)
	}
	if journal.cleared != 1 {
		t.Error("the journal outlived the completed operation")
	}
}

func TestSkipRefusesWhenNothingIsInProgress(t *testing.T) {
	service, _, _ := newService(stackGit(), stack())
	if err := service.Skip(context.Background()); err == nil {
		t.Error("Skip() error = nil with no restack in progress")
	}
}

// The resumable engine moves one line of descent per invocation, so a stack is
// rebased one branch at a time onto the parent it now has. Handing it the whole
// chain and asking --update-refs to carry the intermediate branches works on
// some Git versions and not others.
func TestContinuingIntoAnotherRoundRebasesEachBranchOnItsOwnParent(t *testing.T) {
	git := stackGit()
	git.previewClean = false
	git.inProgress = true
	service, _, journal := newService(git, stack())
	journal.record = Record{Branch: "synthetic-b", Scope: string(graph.ScopeTrunk), Original: map[string]string{}}
	journal.present = true

	if err := service.Continue(context.Background()); err != nil {
		t.Fatalf("Continue() error = %v", err)
	}

	// One rebase per branch, bottom-up, each replaying only its own commits
	// onto the parent it now has.
	if len(git.rebases) != 2 {
		t.Fatalf("rebases = %v, want one per branch", git.rebases)
	}
	if got := git.rebases[0]; got.From != "trunk-old" || got.To != "synthetic-a" {
		t.Errorf("first rebase = %v, want synthetic-a from its own fork point", got)
	}
	if got := git.rebases[1]; got.From != "a-old" || got.To != "synthetic-b" {
		t.Errorf("second rebase = %v, want synthetic-b from its own fork point", got)
	}
}

// A resumed restack has to carry on to the branches above the one that
// conflicted. It used to stop and report success, because the branch it had
// just rewritten no longer matched its recorded fork point and the recomputed
// plan read that as a refusal rather than as work already done.
func TestContinueCarriesOnToTheRestOfTheChain(t *testing.T) {
	git := chainGitMidResume()
	git.inProgress = true
	service, _, journal := newService(git, chainStack())
	journal.record = Record{
		Branch:   "synthetic-c",
		Scope:    string(graph.ScopeTrunk),
		ReturnTo: "synthetic-c",
		Original: map[string]string{"synthetic-b": "b-old", "synthetic-c": "c-old"},
		Reparent: map[string]string{"synthetic-b": "synthetic-trunk"},
	}
	journal.present = true

	if err := service.Continue(context.Background()); err != nil {
		t.Fatalf("Continue() error = %v", err)
	}

	rewritten := append(append([]string{}, rangeTargets(git.rebases)...), replayTargets(git.replays)...)
	if !contains(rewritten, "synthetic-c") {
		t.Errorf("rewrote %v, want synthetic-c carried on to", rewritten)
	}
}

// A branch that becomes collapsible while a resume is in flight has to be
// moved, not driven through an engine that skips it.
//
// finish once drove the engine without collapsing first, so such a branch was
// never moved, the next pass computed an identical plan, and the loop hit its
// own non-convergence guard — a hard failure for a case the design has an
// answer to. Apply had always collapsed first; only the resumable path had
// half the sequence.
func TestResumeCollapsesBranchesWithNothingLeftToContribute(t *testing.T) {
	git := chainGitMidResume()
	git.previewClean = false
	git.collapses = map[string]bool{"synthetic-c": true}
	git.inProgress = true
	service, _, journal := newService(git, chainStack())
	journal.record = Record{
		Branch:   "synthetic-c",
		Scope:    string(graph.ScopeTrunk),
		Original: map[string]string{"synthetic-b": "b-old", "synthetic-c": "c-old"},
		Reparent: map[string]string{"synthetic-b": "synthetic-trunk"},
	}
	journal.present = true

	if err := service.Continue(context.Background()); err != nil {
		t.Fatalf("Continue() error = %v", err)
	}

	// Moved rather than replayed: the engine never sees a commit already in
	// the base.
	if git.restored["synthetic-c"] == "" {
		t.Errorf("synthetic-c was not moved to its base; engine calls were %v", git.rebases)
	}
	if journal.cleared != 1 {
		t.Errorf("journal cleared %d times, want once", journal.cleared)
	}
}

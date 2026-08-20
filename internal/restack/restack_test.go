package restack

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/graph"
)

// A branch whose parent is being rewritten has to be rewritten too, even
// though it still sits exactly where its fork point says. Missing this
// restacks the bottom of a stack and strands everything above it.
func TestPlanIncludesDescendantsOfARewrittenBranch(t *testing.T) {
	service, _, _ := newService(stackGit(), stack())

	plan, err := service.Plan(context.Background(), selection(), Onto{}, false)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	if got := strings.Join(plan.Branches(), ","); got != "synthetic-a,synthetic-b" {
		t.Errorf("Branches() = %s, want both, parent first", got)
	}
}

// The engines replay the union of the ranges onto one base, so a chain has to
// be expressed from a single origin. Per-branch origins ask them to place each
// branch on the base independently, which conflicts immediately.
func TestPlanReplaysEveryRangeFromOneOrigin(t *testing.T) {
	git := stackGit()
	service, _, _ := newService(git, stack())
	plan, err := service.Plan(context.Background(), selection(), Onto{}, false)
	if err != nil {
		t.Fatal(err)
	}

	if err := service.Apply(context.Background(), plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(git.replays) != 1 {
		t.Fatalf("replays = %d, want exactly one", len(git.replays))
	}
	for _, replayed := range git.replays[0] {
		if replayed.From != "trunk-old" {
			t.Errorf("range %v starts at %q, want the topmost fork point", replayed, replayed.From)
		}
	}
}

func TestCleanApplyReplaysAndResyncsTheIndex(t *testing.T) {
	git := stackGit()
	service, store, journal := newService(git, stack())
	plan, err := service.Plan(context.Background(), selection(), Onto{}, false)
	if err != nil {
		t.Fatal(err)
	}

	if err := service.Apply(context.Background(), plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if len(git.rebases) != 0 {
		t.Error("a clean rewrite must not touch the working tree")
	}
	if git.resets != 1 {
		t.Errorf("index resyncs = %d, want one; a replay moves refs without updating the index", git.resets)
	}
	if journal.present {
		t.Error("a clean rewrite journalled an operation that cannot be interrupted")
	}
	// Fork points describe the world after the rewrite, not before it: the
	// parent's new tip, not the commit it no longer points at. While the fake
	// left refs where they were those were the same value, so this asserted
	// nothing.
	if fork := store.graph.Edges["synthetic-b"].ForkPoint; fork != "replayed-synthetic-a" {
		t.Errorf("synthetic-b fork point = %q, want the parent's post-rewrite tip", fork)
	}
	// The checkout is brought to where its branch now points, from where it
	// stood, which is the pair git needs to update the tree at all.
	if got := strings.Join(git.switched, ","); got != "b-old->replayed-synthetic-b" {
		t.Errorf("checkout reconciled as %q, want the old tip to the new one", got)
	}
	if git.pinned["synthetic-a"] == "" {
		t.Error("fork points were not pinned, so they can be collected")
	}
}

// Recording only the branches a plan still lists would record nothing at all
// once the rewrite has succeeded, leaving every fork point describing the
// world before it.
func TestForkPointsAreRecordedForTheWholeSelection(t *testing.T) {
	git := stackGit()
	service, store, _ := newService(git, stack())
	plan, err := service.Plan(context.Background(), selection(), Onto{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Apply(context.Background(), plan); err != nil {
		t.Fatal(err)
	}

	for _, branch := range []string{"synthetic-a", "synthetic-b"} {
		if store.graph.Edges[branch].ForkPoint == "" {
			t.Errorf("%s has no recorded fork point after a rewrite", branch)
		}
	}
}

// A conflicting rewrite is half applied and resumable, so it journals every
// branch's original tip — which git cannot restore, because it only rolls back
// the invocation it is running.
func TestConflictingApplyJournalsOriginalTips(t *testing.T) {
	git := stackGit()
	git.previewClean = false
	git.rebaseErr = errors.New("synthetic conflict")
	service, _, journal := newService(git, stack())
	plan, err := service.Plan(context.Background(), selection(), Onto{}, false)
	if err != nil {
		t.Fatal(err)
	}

	if err := service.Apply(context.Background(), plan); err == nil {
		t.Fatal("Apply() error = nil for a conflicting rewrite")
	}

	if !journal.present {
		t.Fatal("an interrupted rewrite left no journal to resume from")
	}
	for _, branch := range []string{"synthetic-a", "synthetic-b"} {
		if journal.record.Original[branch] == "" {
			t.Errorf("no original tip recorded for %s, so --abort cannot restore it", branch)
		}
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

// Anything whose recorded structure cannot be trusted must not be replayed:
// the range would be computed from a fork point that is not in the branch.
func TestPlanRefusesStatesItCannotComputeARangeFrom(t *testing.T) {
	git := stackGit()
	// synthetic-b was rebased by hand, so its recorded fork point is gone.
	git.ancestors["synthetic-b"] = []string{}
	service, _, _ := newService(git, stack())

	plan, err := service.Plan(context.Background(), selection(), Onto{}, false)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.Blocked == "" {
		t.Fatal("Blocked = \"\" for a branch that moved off its recorded parent")
	}
	if !strings.Contains(plan.Blocked, "retrack") {
		t.Errorf("Blocked = %q, want it to name the remedy", plan.Blocked)
	}
}

func TestApplyRefusesABlockedPlan(t *testing.T) {
	git := stackGit()
	git.ancestors["synthetic-b"] = []string{}
	service, store, _ := newService(git, stack())
	plan, err := service.Plan(context.Background(), selection(), Onto{}, false)
	if err != nil {
		t.Fatal(err)
	}
	before := store.graph.Clone()

	if err := service.Apply(context.Background(), plan); err == nil {
		t.Fatal("Apply() error = nil for a blocked plan")
	}
	if !store.graph.Equal(before) {
		t.Error("a blocked plan changed the graph")
	}
}

// A rewrite that conflicts across a fork would need one rebase per line of
// descent and a journal that tracks which finished, so it refuses instead.
func TestForkedConflictIsRefusedRatherThanHalfDone(t *testing.T) {
	forked := stack()
	forked.Edges["synthetic-c"] = graph.Edge{Parent: "synthetic-a", ForkPoint: "a-old"}
	git := stackGit()
	git.local = append(git.local, "synthetic-c")
	git.objects["synthetic-c"] = "c-old"
	git.ancestors["synthetic-c"] = []string{"a-old", "trunk-old"}
	git.previewClean = false
	service, _, _ := newService(git, forked)

	plan, err := service.Plan(context.Background(), selection(), Onto{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Blocked == "" {
		t.Fatal("Blocked = \"\" for a conflicting rewrite of a forked selection")
	}
	if !strings.Contains(plan.Blocked, "--scope path") {
		t.Errorf("Blocked = %q, want it to name the way forward", plan.Blocked)
	}
}

func TestRequiresAConfiguredService(t *testing.T) {
	if _, err := (Service{}).Plan(context.Background(), selection(), Onto{}, false); err == nil {
		t.Fatal("Plan() error = nil for an unconfigured service")
	}
}

// A repository whose Git cannot replay loses the conflict prediction and
// nothing else, so it still restacks through the resumable engine.
func TestPlanWithoutReplaySupportStillPlans(t *testing.T) {
	git := stackGit()
	git.replaySupported = false
	service, _, _ := newService(git, stack())

	plan, err := service.Plan(context.Background(), selection(), Onto{}, false)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.Clean {
		t.Error("Clean = true without a preview to establish it")
	}
	if len(plan.Steps) == 0 {
		t.Error("no steps planned")
	}
}

// forkedParent is the shape the absorb question exists for: the parent no
// longer has commits the child still carries.
func droppedCommitGit() *fakeGit {
	git := stackGit()
	// synthetic-a's tip moved and the fork point is no longer behind it, so
	// synthetic-b carries a commit synthetic-a has given up.
	git.objects["synthetic-a"] = "a-new"
	git.ancestors["synthetic-b"] = []string{"a-old", "trunk-old"}
	git.ancestors["a-new"] = []string{"trunk-old"}
	git.dropped = map[string][]string{"a-new..a-old": {"dropped-1"}}
	git.behind = map[string]int{"a-new..a-old": 1}
	return git
}

// Keeping commits the parent dropped is a metadata change: the parent's tip is
// already an ancestor of the child, so nothing is rewritten.
func TestAbsorbRewritesNothingAndOnlyMovesTheForkPoint(t *testing.T) {
	git := droppedCommitGit()
	service, store, _ := newService(git, stack())

	plan, err := service.Plan(context.Background(), selection(), Onto{}, true)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.Blocked != "" {
		t.Fatalf("Blocked = %q for an absorbable set", plan.Blocked)
	}
	if err := service.Apply(context.Background(), plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if len(git.replays) != 0 || len(git.rebases) != 0 {
		t.Error("absorbing rewrote commits; it only has to re-record where the branch forks")
	}
	if store.graph.Edges["synthetic-b"].ForkPoint == "" {
		t.Error("the fork point was not re-recorded")
	}
}

// A commit the parent rewrote still exists there under a new object id, so
// keeping the old copy would duplicate it. Only a set that is entirely
// dropped can be absorbed.
func TestAbsorbIsRefusedWhenAnOrphanWasRewrittenRatherThanRemoved(t *testing.T) {
	git := droppedCommitGit()
	// Two commits differ, but only one of them is genuinely gone.
	git.behind["a-new..a-old"] = 2
	service, _, _ := newService(git, stack())

	plan, err := service.Plan(context.Background(), selection(), Onto{}, true)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.Blocked == "" {
		t.Fatal("Blocked = \"\" for a set that cannot be absorbed")
	}
	if !strings.Contains(plan.Blocked, "duplicate") {
		t.Errorf("Blocked = %q, want it to say why", plan.Blocked)
	}
}

func TestPlanReportsOrphansSoTheyAreNeverDroppedSilently(t *testing.T) {
	service, _, _ := newService(droppedCommitGit(), stack())

	plan, err := service.Plan(context.Background(), selection(), Onto{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Orphaned()) == 0 {
		t.Fatal("no orphans reported for a parent that dropped a commit")
	}
	if !plan.Absorbable() {
		t.Error("Absorbable() = false for a set where every orphan is genuinely dropped")
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

// Revalidation is what stops a rewrite acting on a world that moved between
// the preview and the apply.
func TestRevalidateRefusesWhenTheStackMovedUnderneath(t *testing.T) {
	git := stackGit()
	service, _, _ := newService(git, stack())
	preview, err := service.Plan(context.Background(), selection(), Onto{}, false)
	if err != nil {
		t.Fatal(err)
	}

	git.objects["synthetic-b"] = "b-moved"
	if _, err := service.Revalidate(context.Background(), selection(), Onto{}, false, preview); err == nil {
		t.Fatal("Revalidate() error = nil after a branch moved")
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

// A Git that cannot preview must not be reported as having previewed. "We
// could not look" and "we looked and it will conflict" lead a reader to
// different actions.
func TestPlanWithoutReplayReportsThatNothingWasPredicted(t *testing.T) {
	git := stackGit()
	git.replaySupported = false
	service, _, _ := newService(git, stack())

	plan, err := service.Plan(context.Background(), selection(), Onto{}, false)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.Predicted {
		t.Error("Predicted = true without a preview engine")
	}
	if plan.Clean {
		t.Error("Clean = true without a preview to establish it")
	}
	if plan.Blocked != "" {
		t.Errorf("Blocked = %q; an unpredictable rewrite is still allowed to run", plan.Blocked)
	}
}

// Whether a rewrite engine drops an already-upstream commit or reapplies it
// changed between Git 2.54 and 2.55, and it is exactly the case a restack
// exists for. So such commits are never handed to an engine: the branch that
// owns them has nothing left to contribute, and its ref simply moves.
func TestACollapsedBranchIsMovedRatherThanReplayed(t *testing.T) {
	git := stackGit()
	// synthetic-a's work is already in the trunk, as it would be after a
	// squash merge.
	git.collapses = map[string]bool{"synthetic-a": true}
	service, _, _ := newService(git, stack())

	plan, err := service.Plan(context.Background(), selection(), Onto{}, false)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if want := "synthetic-a"; strings.Join(plan.Emptied(), ",") != want {
		t.Errorf("Emptied() = %v, want %s", plan.Emptied(), want)
	}

	if err := service.Apply(context.Background(), plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if git.restored["synthetic-a"] == "" {
		t.Error("the collapsed branch's ref was not moved directly")
	}
	if len(git.replays) != 1 {
		t.Fatalf("replays = %d, want one", len(git.replays))
	}
	for _, replayed := range git.replays[0] {
		if replayed.To == "synthetic-a" {
			t.Errorf("the collapsed branch was handed to the engine: %v", git.replays[0])
		}
	}
}

// A selection where every branch has collapsed needs no engine at all.
func TestAWhollyCollapsedSelectionRunsNoEngine(t *testing.T) {
	git := stackGit()
	git.collapses = map[string]bool{"synthetic-a": true, "synthetic-b": true}
	service, _, _ := newService(git, stack())

	plan, err := service.Plan(context.Background(), selection(), Onto{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Clean {
		t.Error("Clean = false when there is nothing that could conflict")
	}
	if err := service.Apply(context.Background(), plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(git.replays) != 0 || len(git.rebases) != 0 {
		t.Errorf("an engine ran with nothing to replay: replays=%v rebases=%v", git.replays, git.rebases)
	}
}

// Both engines are external and version-dependent, so success is checked
// against the outcome rather than against an exit status. A rewrite that
// quietly left a branch where it was would otherwise look identical to one
// that worked.
func TestApplyRefusesToReportSuccessWhenTheRewriteDidNotHappen(t *testing.T) {
	git := stackGit()
	// The engine returns zero but moves nothing, which is what an unsupported
	// version looks like from here.
	git.replayLeavesRefsAlone = true
	service, _, _ := newService(git, stack())
	plan, err := service.Plan(context.Background(), selection(), Onto{}, false)
	if err != nil {
		t.Fatal(err)
	}

	err = service.Apply(context.Background(), plan)
	if err == nil {
		t.Fatal("Apply() error = nil after an engine that did nothing")
	}
	if !strings.Contains(err.Error(), "did not perform the rewrite") {
		t.Errorf("error = %v, want it to say the rewrite did not happen", err)
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

// An edge written before fork points were recorded carries a parent and
// nothing else. Standing in the parent's current tip for the missing fork
// point makes the replay range empty, so the rewrite does nothing at all —
// and used to report success for it, directly contradicting the graph, which
// went on saying the branch needed restacking.
func TestALegacyEdgeThatHasDriftedIsRefusedRatherThanSilentlyDoingNothing(t *testing.T) {
	git := stackGit()
	legacy := stack()
	legacy.Edges["synthetic-a"] = graph.Edge{Parent: "synthetic-trunk"}
	service, _, _ := newService(git, legacy)

	plan, err := service.Plan(context.Background(), selection(), Onto{}, false)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.Blocked == "" {
		t.Fatalf("Blocked = %q, want a refusal naming the branch with no recorded fork point", plan.Blocked)
	}
	if !strings.Contains(plan.Blocked, "synthetic-a") {
		t.Errorf("Blocked = %q, want it to name synthetic-a", plan.Blocked)
	}
	if len(plan.Steps) != 0 {
		t.Errorf("Steps = %v, want none once the plan is blocked", plan.Branches())
	}
}

// The substitute is sound while the branch still sits on its parent, because
// then the parent's tip really is where the branch's own commits begin. Only
// drift invalidates it, so an aligned legacy edge must stay usable.
func TestALegacyEdgeThatIsStillAlignedIsNotRefused(t *testing.T) {
	git := stackGit()
	// synthetic-a sits exactly on the trunk, so nothing about it has drifted.
	git.objects["synthetic-trunk"] = "trunk-old"
	legacy := stack()
	legacy.Edges["synthetic-a"] = graph.Edge{Parent: "synthetic-trunk"}
	service, _, _ := newService(git, legacy)

	plan, err := service.Plan(context.Background(), selection(), Onto{}, false)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.Blocked != "" {
		t.Fatalf("Blocked = %q for an aligned legacy edge", plan.Blocked)
	}
}

// A rewrite moves a ref without checking anything out, so nothing stopped it
// moving a branch another worktree held. Git updated the ref, that worktree's
// index still described the old commit, and its next git status reported staged
// changes nobody made — while the preview said it had touched no checked-out
// branch.
func TestARewriteRefusesABranchAnotherWorktreeHasCheckedOut(t *testing.T) {
	for _, test := range []struct {
		name      string
		elsewhere map[string]string
		err       error
		blocked   bool
	}{
		{
			name:      "a selected branch is held",
			elsewhere: map[string]string{"synthetic-b": "/synthetic/other"},
			blocked:   true,
		},
		{
			name:      "a branch outside the selection is not a conflict",
			elsewhere: map[string]string{"synthetic-elsewhere": "/synthetic/other"},
		},
		{name: "no other worktree"},
		{
			// Refusing a rewrite that was safe before this check existed would
			// make an unrelated Git failure break restack entirely.
			name: "a failure to ask is not a refusal",
			err:  errors.New("synthetic worktree failure"),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			git := holdingGit{fakeGit: stackGit(), elsewhere: test.elsewhere, err: test.err}
			service, _, _ := newService(git.fakeGit, stack())
			service.Git = git

			plan, err := service.Plan(context.Background(), selection(), Onto{}, false)
			if err != nil {
				t.Fatalf("Plan() error = %v", err)
			}

			if blocked := plan.Blocked != ""; blocked != test.blocked {
				t.Fatalf("Blocked = %q, want blocked=%t", plan.Blocked, test.blocked)
			}
			if !test.blocked {
				return
			}
			for _, want := range []string{"synthetic-b", "/synthetic/other", "another worktree"} {
				if !strings.Contains(plan.Blocked, want) {
					t.Errorf("refusal %q does not name %q", plan.Blocked, want)
				}
			}
			if len(plan.Steps) != 0 {
				t.Errorf("Steps = %v, want none once blocked", plan.Branches())
			}
		})
	}
}

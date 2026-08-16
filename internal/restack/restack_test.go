package restack

import (
	"context"
	"errors"
	"strings"
	"testing"

	localgit "github.com/shhac/gt2gh/internal/git"
	"github.com/shhac/gt2gh/internal/graph"
)

// fakeGit is an injected rewrite boundary. The decision matrix below is about
// which branches get replayed and onto what, which is exactly where spawning a
// process per case buys nothing.
type fakeGit struct {
	current   string
	local     []string
	objects   map[string]string
	ancestors map[string][]string
	dropped   map[string][]string
	behind    map[string]int

	replaySupported bool
	previewClean    bool
	previewUpdates  []localgit.RefUpdate
	rebaseErr       error
	inProgress      bool

	replays  [][]localgit.Range
	rebases  []localgit.Range
	ontos    []string
	resets   int
	pinned   map[string]string
	restored map[string]string
	steps    []string
}

func (f *fakeGit) CurrentBranch(context.Context) (string, error)   { return f.current, nil }
func (f *fakeGit) LocalBranches(context.Context) ([]string, error) { return f.local, nil }
func (f *fakeGit) Clean(context.Context) error                     { return nil }

func (f *fakeGit) AncestorBranches(_ context.Context, target string) ([]string, error) {
	return f.ancestors[target], nil
}

func (f *fakeGit) Divergence(_ context.Context, other, target string) (int, int, error) {
	return 1, f.behind[other+".."+target], nil
}

func (f *fakeGit) IsAncestor(_ context.Context, ancestor, descendant string) (bool, error) {
	resolved := ancestor
	if object, listed := f.objects[ancestor]; listed {
		resolved = object
	}
	for _, candidate := range f.ancestors[descendant] {
		if candidate == ancestor || candidate == resolved || f.objects[candidate] == resolved {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeGit) Resolve(_ context.Context, revision string) (string, error) {
	if resolved, listed := f.objects[revision]; listed {
		return resolved, nil
	}
	return revision, nil
}

func (f *fakeGit) SupportsReplay(context.Context) (bool, error) { return f.replaySupported, nil }

func (f *fakeGit) PreviewReplay(_ context.Context, onto string, ranges []localgit.Range) ([]localgit.RefUpdate, bool, error) {
	f.ontos = append(f.ontos, onto)
	return f.previewUpdates, f.previewClean, nil
}

func (f *fakeGit) Replay(_ context.Context, onto string, ranges []localgit.Range) error {
	f.replays = append(f.replays, ranges)
	f.ontos = append(f.ontos, onto)
	// Model what the rewrite does to the repository, not just that it was
	// asked for: every replayed branch now descends from the new base, which
	// is what the fork points recorded afterwards are checked against.
	for _, replayed := range ranges {
		f.ancestors[replayed.To] = append(f.ancestors[replayed.To], onto)
	}
	return nil
}

func (f *fakeGit) Rebase(_ context.Context, onto string, replayed localgit.Range) error {
	f.rebases = append(f.rebases, replayed)
	f.ontos = append(f.ontos, onto)
	if f.rebaseErr != nil {
		f.inProgress = true
	}
	return f.rebaseErr
}

func (f *fakeGit) RebaseContinue(context.Context) error {
	f.steps = append(f.steps, "continue")
	f.inProgress = false
	return nil
}

func (f *fakeGit) RebaseAbort(context.Context) error {
	f.steps = append(f.steps, "abort")
	f.inProgress = false
	return nil
}

func (f *fakeGit) RebaseSkip(context.Context) error {
	f.steps = append(f.steps, "skip")
	f.inProgress = false
	return nil
}

func (f *fakeGit) RebaseInProgress(context.Context) (bool, error) { return f.inProgress, nil }

func (f *fakeGit) ResetKeep(context.Context) error { f.resets++; return nil }

func (f *fakeGit) CherryDropped(_ context.Context, upstream, head string) ([]string, error) {
	return f.dropped[upstream+".."+head], nil
}

func (f *fakeGit) PinForkPoint(_ context.Context, branch, object string) error {
	if f.pinned == nil {
		f.pinned = map[string]string{}
	}
	f.pinned[branch] = object
	return nil
}

func (f *fakeGit) UpdateBranch(_ context.Context, branch, object string) error {
	if f.restored == nil {
		f.restored = map[string]string{}
	}
	f.restored[branch] = object
	return nil
}

// memoryStore stands in for the graph store.
type memoryStore struct{ graph graph.Graph }

func (m *memoryStore) Load(context.Context) (graph.Graph, error) { return m.graph.Clone(), nil }
func (m *memoryStore) Save(_ context.Context, g graph.Graph) error {
	m.graph = g.Clone()
	return nil
}
func (m *memoryStore) Path(context.Context) (string, error) { return "/synthetic/graph.json", nil }

// memoryJournal records what survives between invocations.
type memoryJournal struct {
	record  Record
	present bool
	cleared int
}

func (j *memoryJournal) Load(context.Context) (Record, bool, error) {
	return j.record, j.present, nil
}
func (j *memoryJournal) Save(_ context.Context, record Record) error {
	j.record, j.present = record, true
	return nil
}
func (j *memoryJournal) Clear(context.Context) error { j.cleared++; j.present = false; return nil }

// stack is synthetic-trunk <- synthetic-a <- synthetic-b, where the trunk has
// moved on since both were adopted.
func stack() graph.Graph {
	return graph.Graph{
		Edges: map[string]graph.Edge{
			"synthetic-a": {Parent: "synthetic-trunk", ForkPoint: "trunk-old", Authority: graph.AuthorityG2G},
			"synthetic-b": {Parent: "synthetic-a", ForkPoint: "a-old", Authority: graph.AuthorityG2G},
		},
		Trunks: []string{"synthetic-trunk"},
	}
}

func stackGit() *fakeGit {
	return &fakeGit{
		current: "synthetic-b",
		local:   []string{"synthetic-trunk", "synthetic-a", "synthetic-b"},
		objects: map[string]string{
			"synthetic-trunk": "trunk-new",
			"synthetic-a":     "a-old",
			"synthetic-b":     "b-old",
			"trunk-old":       "trunk-old",
			"a-old":           "a-old",
		},
		ancestors: map[string][]string{
			"synthetic-a": {"trunk-old"},
			"synthetic-b": {"a-old", "trunk-old"},
		},
		replaySupported: true,
		previewClean:    true,
	}
}

func newService(git *fakeGit, adopted graph.Graph) (Service, *memoryStore, *memoryJournal) {
	store := &memoryStore{graph: adopted}
	journal := &memoryJournal{}
	return Service{
		Git:     git,
		Graph:   graph.Service{Git: git, Store: store},
		Journal: journal,
	}, store, journal
}

func selection() graph.Selection {
	return graph.Selection{Branch: "synthetic-b", Scope: graph.ScopeGraph}
}

// A branch whose parent is being rewritten has to be rewritten too, even
// though it still sits exactly where its fork point says. Missing this
// restacks the bottom of a stack and strands everything above it.
func TestPlanIncludesDescendantsOfARewrittenBranch(t *testing.T) {
	service, _, _ := newService(stackGit(), stack())

	plan, err := service.Plan(context.Background(), selection(), "", false)
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
	plan, err := service.Plan(context.Background(), selection(), "", false)
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
	plan, err := service.Plan(context.Background(), selection(), "", false)
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
	// Fork points describe the world after the rewrite, not before it.
	if fork := store.graph.Edges["synthetic-b"].ForkPoint; fork != "a-old" {
		t.Errorf("synthetic-b fork point = %q", fork)
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
	plan, err := service.Plan(context.Background(), selection(), "", false)
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
	plan, err := service.Plan(context.Background(), selection(), "", false)
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
	journal.record = Record{Branch: "synthetic-b", Scope: string(graph.ScopeGraph), Original: map[string]string{}}
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
	journal.record = Record{Branch: "synthetic-b", Scope: string(graph.ScopeGraph), Original: map[string]string{}}
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

	plan, err := service.Plan(context.Background(), selection(), "", false)
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
	plan, err := service.Plan(context.Background(), selection(), "", false)
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

	plan, err := service.Plan(context.Background(), selection(), "", false)
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
	if _, err := (Service{}).Plan(context.Background(), selection(), "", false); err == nil {
		t.Fatal("Plan() error = nil for an unconfigured service")
	}
}

// A repository whose Git cannot replay loses the conflict prediction and
// nothing else, so it still restacks through the resumable engine.
func TestPlanWithoutReplaySupportStillPlans(t *testing.T) {
	git := stackGit()
	git.replaySupported = false
	service, _, _ := newService(git, stack())

	plan, err := service.Plan(context.Background(), selection(), "", false)
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

	plan, err := service.Plan(context.Background(), selection(), "", true)
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

	plan, err := service.Plan(context.Background(), selection(), "", true)
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

	plan, err := service.Plan(context.Background(), selection(), "", false)
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
	journal.record = Record{Branch: "synthetic-b", Scope: string(graph.ScopeGraph), Original: map[string]string{}}
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
	preview, err := service.Plan(context.Background(), selection(), "", false)
	if err != nil {
		t.Fatal(err)
	}

	git.objects["synthetic-b"] = "b-moved"
	if _, err := service.Revalidate(context.Background(), selection(), "", false, preview); err == nil {
		t.Fatal("Revalidate() error = nil after a branch moved")
	}
}

// Resuming into a second conflicting round must hand the engine a range from
// the topmost fork point, exactly as the first round did. Using the last
// step's own range replays only the top branch's commits and drops everything
// below it.
func TestContinuingIntoAnotherRoundReplaysFromTheTopmostForkPoint(t *testing.T) {
	git := stackGit()
	git.previewClean = false
	git.inProgress = true
	service, _, journal := newService(git, stack())
	journal.record = Record{Branch: "synthetic-b", Scope: string(graph.ScopeGraph), Original: map[string]string{}}
	journal.present = true

	if err := service.Continue(context.Background()); err != nil {
		t.Fatalf("Continue() error = %v", err)
	}

	if len(git.rebases) != 1 {
		t.Fatalf("rebases = %d, want one more round", len(git.rebases))
	}
	if got := git.rebases[0]; got.From != "trunk-old" {
		t.Errorf("range starts at %q, want the topmost fork point trunk-old", got.From)
	}
	if got := git.rebases[0]; got.To != "synthetic-b" {
		t.Errorf("range ends at %q, want the tip of the chain", got.To)
	}
}

// A Git that cannot preview must not be reported as having previewed. "We
// could not look" and "we looked and it will conflict" lead a reader to
// different actions.
func TestPlanWithoutReplayReportsThatNothingWasPredicted(t *testing.T) {
	git := stackGit()
	git.replaySupported = false
	service, _, _ := newService(git, stack())

	plan, err := service.Plan(context.Background(), selection(), "", false)
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

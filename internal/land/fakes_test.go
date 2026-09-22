package land

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/prune"
	"github.com/shhac/g2g/internal/push"
	"github.com/shhac/g2g/internal/stack"
	syncer "github.com/shhac/g2g/internal/sync"
)

// The world landing acts on, injected.
//
// Landing's own job is ordering, and ordering is a decision matrix: spawning a
// process per case would buy nothing. What a fake cannot vouch for -- whether a
// branch replays cleanly onto a squashed trunk -- is checked against a real
// repository elsewhere.

// events records everything that happened, in order, across every fake. It is
// one slice because the order between them is the thing under test: a push has
// to precede the merge it publishes for, and the merge has to precede the sync
// that replays onto its result.
type events struct{ seen []string }

func (e *events) record(what string) { e.seen = append(e.seen, what) }

func (e *events) only(prefix string) []string {
	matched := make([]string, 0, len(e.seen))
	for _, event := range e.seen {
		if strings.HasPrefix(event, prefix) {
			matched = append(matched, event)
		}
	}
	return matched
}

func (e *events) index(want string) int { return slices.Index(e.seen, want) }

type fakeGit struct {
	events    *events
	current   string
	local     []string
	tips      map[string]string
	objects   map[string]string
	ancestors map[string][]string
	absorbed  map[string]bool
	dirty     error
	deleteErr error
	switchErr error
}

func (f *fakeGit) CurrentBranch(context.Context) (string, error)   { return f.current, nil }
func (f *fakeGit) LocalBranches(context.Context) ([]string, error) { return f.local, nil }
func (f *fakeGit) Clean(context.Context) error                     { return f.dirty }

func (f *fakeGit) Resolve(_ context.Context, revision string) (string, error) {
	if object, listed := f.objects[revision]; listed {
		return object, nil
	}
	return "", fmt.Errorf("revision %q is not a commit in this repository", revision)
}

func (f *fakeGit) IsAncestor(_ context.Context, ancestor, descendant string) (bool, error) {
	return slices.Contains(f.ancestors[descendant], ancestor), nil
}

// RemoteTips answers nothing on a context that is done, as a spawned process
// does. A fake that answered anyway hid a diagnosis that could never run.
func (f *fakeGit) RemoteTips(ctx context.Context, _ string, branches []string) (map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tips := map[string]string{}
	for _, branch := range branches {
		if tip, published := f.tips[branch]; published {
			tips[branch] = tip
		}
	}
	return tips, nil
}

func (f *fakeGit) FetchIsolated(_ context.Context, _ string, branches []string) error {
	f.events.record("fetch:" + strings.Join(branches, ","))
	return nil
}

func (f *fakeGit) Cherry(_ context.Context, upstream, head, _ string) ([]string, []string, error) {
	if f.absorbed[head] {
		return nil, []string{head}, nil
	}
	return []string{head + "-commit"}, nil, nil
}

func (f *fakeGit) Absorbed(_ context.Context, _, branch string) (bool, error) {
	return f.absorbed[branch], nil
}

func (f *fakeGit) DeleteBranch(_ context.Context, branch string) error {
	f.events.record("delete-local:" + branch)
	return f.deleteErr
}

func (f *fakeGit) DeleteRemoteBranch(_ context.Context, _, branch string) error {
	f.events.record("delete-remote:" + branch)
	return f.deleteErr
}

func (f *fakeGit) SwitchBranch(_ context.Context, branch string) error {
	f.events.record("switch:" + branch)
	if f.switchErr != nil {
		return f.switchErr
	}
	f.current = branch
	return nil
}

type fakeGitHub struct {
	events *events
	prs    []githubstack.PullRequest
	// states is what Mergeability answers, and merging mutates it, so a settle
	// that asks after a merge sees what GitHub would say.
	states   map[int]githubstack.MergeState
	allowed  githubstack.Allowed
	asked    int
	mergeErr error
	// settleOn runs after the first question, standing in for GitHub catching
	// up with a push it had not yet observed.
	settleOn func()
}

func (f *fakeGitHub) Inspect(_ context.Context, _ []string) ([]githubstack.PullRequest, error) {
	return f.prs, nil
}

func (f *fakeGitHub) Mergeability(_ context.Context, numbers []int) (githubstack.Mergeability, error) {
	f.asked++
	if f.asked > 1 && f.settleOn != nil {
		f.settleOn()
		f.settleOn = nil
	}
	states := map[int]githubstack.MergeState{}
	for _, number := range numbers {
		states[number] = f.states[number]
	}
	return githubstack.Mergeability{Allowed: f.allowed, States: states}, nil
}

func (f *fakeGitHub) Merge(_ context.Context, number int, method githubstack.Method, admin bool) error {
	f.events.record(fmt.Sprintf("merge:%d:%s:admin=%t", number, method, admin))
	if f.mergeErr != nil {
		return f.mergeErr
	}
	state := f.states[number]
	state.State = "MERGED"
	state.MergeCommit = fmt.Sprintf("merge-%d", number)
	f.states[number] = state
	for index, pr := range f.prs {
		if pr.Number == number {
			f.prs[index].State = "MERGED"
		}
	}
	return nil
}

func (f *fakeGitHub) Retarget(_ context.Context, number int, base string) error {
	f.events.record(fmt.Sprintf("retarget:%d:%s", number, base))
	state := f.states[number]
	state.Base = base
	f.states[number] = state
	for index, pr := range f.prs {
		if pr.Number == number {
			f.prs[index].Base = base
		}
	}
	return nil
}

type fakePusher struct {
	events  *events
	git     *fakeGit
	blocked string
	planErr error
	// extra names a branch the path selection picks up beside the one asked
	// for, which is what an untidy earlier cycle leaves behind.
	extra string
	// silent is a push that reports success and moves nothing — a lease
	// refused, a hook that dropped it. It is a real outcome, and the wait
	// afterwards has to survive it rather than blame GitHub for it.
	silent bool
	// level is a remote that already holds every branch exactly, so there is
	// nothing to publish.
	level bool
}

func (f *fakePusher) Plan(_ context.Context, selection stack.Selection, _ string) (push.Plan, error) {
	if f.planErr != nil {
		return push.Plan{}, f.planErr
	}
	branches := []string{selection.Branch}
	if f.extra != "" {
		branches = append([]string{f.extra}, branches...)
	}
	plan := push.Plan{Blocked: f.blocked}
	plan.Snapshot = stack.Snapshot{Branches: branches, Base: selection.Trunk}
	plan.Publishing = map[string]push.Publication{selection.Branch: {Ours: 1}}
	if f.level {
		plan.Publishing[selection.Branch] = push.Publication{}
	}
	return plan, nil
}

func (f *fakePusher) Execute(_ context.Context, plan push.Plan) error {
	f.events.record("push:" + strings.Join(plan.Branches, ","))
	if f.silent || f.git == nil {
		return nil
	}
	for _, branch := range plan.Branches {
		f.git.tips[branch] = f.git.objects[branch]
	}
	return nil
}

type fakeSyncer struct {
	events  *events
	blocked string
	nothing bool
}

func (f *fakeSyncer) Plan(_ context.Context, selection graph.Selection, _ string, _ syncer.Take) (syncer.Plan, error) {
	plan := syncer.Plan{Blocked: f.blocked, Base: "synthetic-main", Advance: !f.nothing}
	plan.Restack.Discovery = graph.Discovery{Target: selection.Branch}
	return plan, nil
}

func (f *fakeSyncer) Apply(_ context.Context, plan syncer.Plan) error {
	f.events.record("sync:" + plan.Restack.Discovery.Target)
	return nil
}

type fakePruner struct {
	events *events
	store  *memoryStore
}

func (f *fakePruner) Plan(_ context.Context, selection graph.Selection) (prune.Plan, error) {
	plan := prune.Plan{Landed: []string{selection.Branch}}
	plan.Discovery = graph.Discovery{Target: selection.Branch}
	return plan, nil
}

func (f *fakePruner) Apply(_ context.Context, plan prune.Plan) error {
	f.events.record("prune:" + strings.Join(plan.Landed, ","))
	f.store.graph = f.store.graph.Untrack(plan.Landed...)
	return nil
}

type memoryStore struct{ graph graph.Graph }

func (m *memoryStore) Load(context.Context) (graph.Graph, error) { return m.graph.Clone(), nil }
func (m *memoryStore) Save(_ context.Context, g graph.Graph) error {
	m.graph = g.Clone()
	return nil
}
func (m *memoryStore) Path(context.Context) (string, error) { return "/synthetic/graph.json", nil }

// fakeSelector answers with a fixed linear path, which is what every case here
// varies the contents of rather than the shape.
type fakeSelector struct {
	snapshot stack.Snapshot
	err      error
}

func (f fakeSelector) Select(context.Context, stack.Selection, string) (stack.Snapshot, error) {
	return f.snapshot, f.err
}

// instant is the clock these tests run on: no wall time, but bounded.
//
// A pauser that only ever returns nil makes settle spin forever on a condition
// that never becomes true — which is not what the real one does, because it
// stops when the context does. A test for a wait that never settles hung
// instead of failing, so the fake gives up the way the budget would.
//
// It does not end the context, which the real budget does. A test of anything
// that happens after a wait gives up has to end it itself, or it proves that
// work runs on a context production has already cancelled.
func instant(context.Context, time.Duration) error {
	attempts.Add(1)
	if attempts.Load() > 200 {
		return context.DeadlineExceeded
	}
	return nil
}

var attempts atomic.Int64

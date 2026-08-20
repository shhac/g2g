package restack

import (
	"context"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/graph"
)

// The rewrite boundary, the two stores, and the fixtures every test here builds
// from.
//
// It was 255 lines at the top of restack_test.go, ahead of three source files'
// worth of tests. internal/cli/fakes_test.go is the same idea.

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
	// collapses names branches whose own work is already in their new base.
	collapses  map[string]bool
	conflicted []string

	replaySupported       bool
	replayLeavesRefsAlone bool
	previewClean          bool
	previewUpdates        []localgit.RefUpdate
	rebaseErr             error
	inProgress            bool

	replays  [][]localgit.Range
	rebases  []localgit.Range
	ontos    []string
	resets   int
	switched []string
	// tips is where a branch points after a rewrite, which is not the same
	// question as what objects the repository has.
	tips     map[string]string
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
	if moved, rewritten := f.tips[revision]; rewritten {
		return moved, nil
	}
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
	if f.replayLeavesRefsAlone {
		return nil
	}
	for _, replayed := range ranges {
		f.ancestors[replayed.To] = append(f.ancestors[replayed.To], onto)
		// The ref moves. Leaving it where it was hid the whole reason the
		// checkout needs reconciling afterwards: nothing appeared to change,
		// so nothing appeared to need bringing up to date. It is kept apart
		// from objects so ancestry keeps answering about the shape, which is a
		// different question from where a branch now points.
		if f.tips == nil {
			f.tips = map[string]string{}
		}
		f.tips[replayed.To] = "replayed-" + replayed.To
	}
	return nil
}

func (f *fakeGit) Rebase(_ context.Context, onto string, replayed localgit.Range) error {
	f.rebases = append(f.rebases, replayed)
	f.ontos = append(f.ontos, onto)
	if f.rebaseErr != nil {
		f.inProgress = true
		return f.rebaseErr
	}
	// Model what the rewrite does to the repository, not just that it was
	// asked for, exactly as Replay does: the branch now descends from the base
	// it was moved onto, which is what verify and the next plan check.
	f.ancestors[replayed.To] = append(f.ancestors[replayed.To], onto)
	return nil
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

func (f *fakeGit) ConflictedPaths(context.Context) ([]string, error) { return f.conflicted, nil }

// SwitchTree records the two ends it was asked to reconcile, which is the only
// way to see that the checkout was brought to the branch's new tip rather than
// left describing the old one.
func (f *fakeGit) SwitchTree(_ context.Context, from, to string) error {
	f.resets++
	f.switched = append(f.switched, from+"->"+to)
	return nil
}

func (f *fakeGit) CherryDropped(_ context.Context, upstream, head string) ([]string, error) {
	return f.dropped[upstream+".."+head], nil
}

// Cherry answers what a branch still contributes. Unlisted means "one commit
// of its own", which keeps cases that are not about collapsing free of noise.
func (f *fakeGit) Cherry(_ context.Context, upstream, head, limit string) ([]string, []string, error) {
	if f.collapses[head] {
		return nil, []string{"already-upstream"}, nil
	}
	return []string{"own-" + head}, nil, nil
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
	// Model what moving a ref does to the repository, not just that it was
	// asked for, exactly as Replay and Rebase do: the branch now points at the
	// object it was moved to, and a later plan measures against that.
	resolved := object
	if listed, ok := f.objects[object]; ok {
		resolved = listed
	}
	f.objects[branch] = resolved
	f.ancestors[branch] = append(f.ancestors[branch], resolved)
	delete(f.collapses, branch)
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
			"synthetic-a": {Parent: "synthetic-trunk", ForkPoint: "trunk-old"},
			"synthetic-b": {Parent: "synthetic-a", ForkPoint: "a-old"},
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
	return graph.Selection{Branch: "synthetic-b", Scope: graph.ScopeTrunk}
}

// chainStack is three deep, so a stop part-way leaves work above it.
func chainStack() graph.Graph {
	return graph.Graph{
		Edges: map[string]graph.Edge{
			// a has already been dealt with: its work is upstream and it sits
			// on the new trunk, which is the state a resume finds it in.
			"synthetic-a": {Parent: "synthetic-trunk", ForkPoint: "trunk-new"},
			"synthetic-b": {Parent: "synthetic-a", ForkPoint: "a-old"},
			"synthetic-c": {Parent: "synthetic-b", ForkPoint: "b-old"},
		},
		Trunks: []string{"synthetic-trunk"},
	}
}

// chainGitMidResume is the repository as a resumed restack finds it: the
// interrupted branch has just been rewritten onto the new trunk, so its
// recorded fork point is no longer in it, while everything above is untouched.
func chainGitMidResume() *fakeGit {
	return &fakeGit{
		current: "synthetic-b",
		local:   []string{"synthetic-trunk", "synthetic-a", "synthetic-b", "synthetic-c"},
		objects: map[string]string{
			"synthetic-trunk": "trunk-new",
			"synthetic-a":     "a-old",
			"synthetic-b":     "b-new",
			"synthetic-c":     "c-old",
			"trunk-old":       "trunk-old",
			"trunk-new":       "trunk-new",
			"a-old":           "a-old",
			"b-old":           "b-old",
		},
		ancestors: map[string][]string{
			"synthetic-a": {"trunk-new"},
			// b was rewritten: it descends from the new trunk and no longer
			// carries the fork point the graph records for it.
			"synthetic-b": {"trunk-new"},
			// c is untouched, so it still sits on the pre-rewrite b.
			"synthetic-c": {"b-old", "a-old", "trunk-old"},
		},
		replaySupported: true,
		previewClean:    true,
	}
}

func rangeTargets(ranges []localgit.Range) []string {
	targets := make([]string, 0, len(ranges))
	for _, r := range ranges {
		targets = append(targets, r.To)
	}
	return targets
}

func replayTargets(replays [][]localgit.Range) []string {
	targets := make([]string, 0)
	for _, batch := range replays {
		targets = append(targets, rangeTargets(batch)...)
	}
	return targets
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// holdingGit reports branches other worktrees have checked out.
type holdingGit struct {
	*fakeGit
	elsewhere map[string]string
	err       error
}

func (g holdingGit) CheckedOutElsewhere(context.Context) (map[string]string, error) {
	return g.elsewhere, g.err
}

package push

import (
	"context"
	"errors"
	"fmt"
	"github.com/shhac/g2g/internal/stack"
	"github.com/shhac/g2g/internal/testutil"
	"strings"
	"testing"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/graphite"
	"github.com/shhac/g2g/internal/link"
	"github.com/shhac/g2g/internal/testutil/forest"
)

func TestPlanTargetsCurrentOrExplicitBranchWithoutCheckout(t *testing.T) {
	for _, test := range []struct {
		name      string
		selection link.Selection
		want      string
	}{
		// The default reaches the tip: standing on middle, top is part of the
		// stack being pushed. The old fake could not express that, so this case
		// used to assert the fake rather than the behaviour.
		{"current", link.Selection{}, "lower,middle,top"},
		{"explicit", link.Selection{Branch: "top"}, "lower,middle,top"},
	} {
		t.Run(test.name, func(t *testing.T) {
			git := &fakeGit{current: "middle", branches: []string{"main", "lower", "middle", "top"}}
			plan, err := Service{Git: git, Selector: graphiteSelector(git, fakeGraphite{paths: paths()})}.Plan(context.Background(), test.selection, "origin")
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(plan.Branches, ","); got != test.want || git.pushes != 0 {
				t.Errorf("branches=%q pushes=%d", got, git.pushes)
			}
			if test.selection.Branch != "" && git.currentCalls != 0 {
				t.Errorf("explicit target read current branch %d times", git.currentCalls)
			}
		})
	}
}

func TestPlanStackExpandsFullLinearPathOrRejectsFork(t *testing.T) {
	git := &fakeGit{current: "middle", branches: []string{"main", "lower", "middle", "top"}}
	service := Service{Git: git, Selector: graphiteSelector(git, fakeGraphite{paths: paths(), stackPaths: map[string]graphite.Stack{"middle": {Path: []string{"main", "lower", "middle", "top"}, Trunks: []string{"main"}}}})}
	plan, err := service.Plan(context.Background(), link.Selection{}, "origin")
	if err != nil || strings.Join(plan.Branches, ",") != "lower,middle,top" {
		t.Fatalf("Plan() = (%#v, %v)", plan, err)
	}
	// A fork is no longer refused during selection — reading one is ordinary —
	// so the refusal belongs here, where a linear projection is the thing that
	// cannot represent it.
	forked := fakeGraphite{paths: map[string]graphite.Stack{
		"middle": {Path: []string{"main", "lower", "middle"}, Trunks: []string{"main"}},
		"top":    {Path: []string{"main", "lower", "middle", "top"}, Trunks: []string{"main"}},
		"side":   {Path: []string{"main", "lower", "middle", "side"}, Trunks: []string{"main"}},
	}}
	git.branches = append(git.branches, "side")
	service.Selector = graphiteSelector(git, forked)
	_, err = service.Plan(context.Background(), link.Selection{}, "origin")
	if err == nil || !strings.Contains(err.Error(), "one ordered path") {
		t.Fatalf("Plan() fork error = %v", err)
	}
	if !strings.Contains(err.Error(), "--branch") {
		t.Errorf("fork refusal does not name the remedy: %v", err)
	}
}

func TestApplyRevalidatesThenMakesOneAtomicLeasePush(t *testing.T) {
	git := &fakeGit{current: "middle", branches: []string{"main", "lower", "middle", "top"}}
	service := Service{Git: git, Selector: graphiteSelector(git, fakeGraphite{paths: paths(), stackPaths: map[string]graphite.Stack{"middle": {Path: []string{"main", "lower", "middle", "top"}, Trunks: []string{"main"}}}})}
	selection := link.Selection{}
	preview, err := service.Plan(context.Background(), selection, "origin")
	if err != nil {
		t.Fatal(err)
	}
	validated, err := service.Revalidate(context.Background(), selection, "origin", preview)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(context.Background(), validated); err != nil {
		t.Fatal(err)
	}
	if git.pushes != 1 || git.remote != "origin" || strings.Join(git.pushed, ",") != "lower,middle,top" {
		t.Errorf("pushes=%d remote=%q branches=%v", git.pushes, git.remote, git.pushed)
	}
}

func TestRevalidateRefusesAChangedPushPlanBeforeMutation(t *testing.T) {
	git := &fakeGit{current: "middle", branches: []string{"main", "lower", "middle", "top"}}
	graphite := &changingGraphite{first: paths()["middle"], next: graphite.Stack{Path: []string{"main", "lower", "middle", "top"}, Trunks: []string{"main"}}}
	service := Service{Git: git, Selector: graphiteSelector(git, graphite)}
	preview, err := service.Plan(context.Background(), link.Selection{}, "origin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Revalidate(context.Background(), link.Selection{}, "origin", preview); err == nil || !strings.Contains(err.Error(), "changed during revalidation") {
		t.Fatalf("Revalidate() error = %v", err)
	}
	if git.pushes != 0 {
		t.Errorf("pushes=%d, want 0", git.pushes)
	}
}

func TestApplyRefusesChangedPlanAndRemoteOrPushFailures(t *testing.T) {
	for _, test := range []struct {
		name      string
		git       *fakeGit
		graphite  fakeGraphite
		wantError string
	}{
		{"missing remote", &fakeGit{current: "middle", branches: []string{"main", "lower", "middle", "top"}, remoteErr: errors.New("unknown remote")}, fakeGraphite{paths: paths()}, "unknown remote"},
		{"lease rejection", &fakeGit{current: "middle", branches: []string{"main", "lower", "middle", "top"}, pushErr: errors.New("lease rejected")}, fakeGraphite{paths: paths()}, "lease rejected"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := Service{Git: test.git, Selector: graphiteSelector(test.git, test.graphite)}
			if test.name == "missing remote" {
				if _, err := service.Plan(context.Background(), link.Selection{}, "origin"); err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("Plan() error = %v", err)
				}
				return
			}
			preview, err := service.Plan(context.Background(), link.Selection{}, "origin")
			if err != nil {
				t.Fatal(err)
			}
			validated, err := service.Revalidate(context.Background(), link.Selection{}, "origin", preview)
			if err != nil {
				t.Fatal(err)
			}
			if err := service.Execute(context.Background(), validated); err == nil || !strings.Contains(err.Error(), test.wantError) || test.git.pushes != 1 {
				t.Errorf("Execute() error=%v pushes=%d", err, test.git.pushes)
			}
		})
	}
}

func TestPlanRejectsEmptyRemote(t *testing.T) {
	service := Service{
		Git:      &fakeGit{current: "middle", branches: []string{"main", "lower", "middle"}, remoteErr: errors.New("remote name must be nonempty")},
		Selector: graphiteSelector(&fakeGit{current: "middle", branches: []string{"main", "lower", "middle"}, remoteErr: errors.New("remote name must be nonempty")}, fakeGraphite{paths: paths()}),
	}
	if _, err := service.Plan(context.Background(), link.Selection{}, ""); err == nil || !strings.Contains(err.Error(), "nonempty") {
		t.Fatalf("Plan() error = %v", err)
	}
}

func TestPlanRejectsOptionLikeGraphiteBranch(t *testing.T) {
	git := &fakeGit{current: "tip", branches: []string{"main", "-synthetic-option", "tip"}}
	service := Service{Git: git, Selector: graphiteSelector(git, fakeGraphite{paths: map[string]graphite.Stack{
		"tip": {Path: []string{"main", "-synthetic-option", "tip"}, Trunks: []string{"main"}},
	}})}
	if _, err := service.Plan(context.Background(), link.Selection{Scope: stack.ScopePath}, "origin"); err == nil || !strings.Contains(err.Error(), "cannot be passed safely to git push") {
		t.Fatalf("Plan() error = %v", err)
	}
	if git.pushes != 0 {
		t.Errorf("pushes=%d, want 0", git.pushes)
	}
}

type fakeGit struct {
	tips                 map[string]string
	leases               []localgit.Lease
	current, remote      string
	branches, pushed     []string
	remoteErr, pushErr   error
	pushes, currentCalls int
}

func (f *fakeGit) CurrentBranch(context.Context) (string, error) {
	f.currentCalls++
	return f.current, nil
}
func (f *fakeGit) LocalBranches(context.Context) ([]string, error) { return f.branches, nil }
func (f *fakeGit) Remote(context.Context, string) error            { return f.remoteErr }
func (f *fakeGit) RemoteTips(_ context.Context, _ string, branches []string) (map[string]string, error) {
	if f.tips != nil {
		return f.tips, nil
	}
	return testutil.RemoteTips(branches), nil
}

func (f *fakeGit) PushAtomic(_ context.Context, remote string, leases []localgit.Lease) error {
	branches := make([]string, 0, len(leases))
	for _, lease := range leases {
		branches = append(branches, lease.Branch)
		f.leases = append(f.leases, lease)
	}
	f.pushes++
	f.remote = remote
	f.pushed = append([]string(nil), branches...)
	return f.pushErr
}

type fakeGraphite struct {
	paths      map[string]graphite.Stack
	stackPaths map[string]graphite.Stack
	stackErr   error
}

func (f fakeGraphite) Discover(ctx context.Context, branch string) (graphite.Stack, error) {
	return f.DiscoverStack(ctx, branch, false)
}
func (f fakeGraphite) DiscoverStack(_ context.Context, branch string, stack bool) (graphite.Stack, error) {
	if stack && f.stackErr != nil {
		return graphite.Stack{}, f.stackErr
	}
	if stack && f.stackPaths != nil {
		return f.stackPaths[branch], nil
	}
	return f.paths[branch], nil
}
func (fakeGraphite) TrackedBranches(context.Context) ([]string, error) { return nil, nil }

type changingGraphite struct {
	first, next graphite.Stack
	calls       int
}

func (f *changingGraphite) DiscoverStack(_ context.Context, _ string, _ bool) (graphite.Stack, error) {
	f.calls++
	if f.calls == 1 {
		return f.first, nil
	}
	return f.next, nil
}

func paths() map[string]graphite.Stack {
	return map[string]graphite.Stack{
		"middle": {Path: []string{"main", "lower", "middle"}, Trunks: []string{"main"}},
		"top":    {Path: []string{"main", "lower", "middle", "top"}, Trunks: []string{"main"}},
	}
}

// A bare --force-with-lease takes its baseline from the remote-tracking ref,
// so what it protects depends on when the user last fetched. Naming the tip
// the plan observed makes the push assert exactly what the preview showed.
func TestPlanPinsTheObservedRemoteTipsAsLeases(t *testing.T) {
	git := &fakeGit{current: "beta", branches: []string{"main", "alpha", "beta"},
		tips: map[string]string{"alpha": "aaa111", "beta": "bbb222"}}
	service := Service{Git: git, Selector: graphiteSelector(git, fakeGraphite{stackPaths: map[string]graphite.Stack{"beta": {Path: []string{"main", "alpha", "beta"}, Trunks: []string{"main"}}}})}

	plan, err := service.Plan(context.Background(), link.Selection{}, "origin")
	if err != nil {
		t.Fatal(err)
	}
	for _, lease := range plan.Leases() {
		if lease.Expected != git.tips[lease.Branch] {
			t.Errorf("%s lease = %q, want the observed tip %q", lease.Branch, lease.Expected, git.tips[lease.Branch])
		}
	}
}

// A branch that moved on the remote between preview and apply must stop the
// push rather than be overwritten by it.
func TestRevalidateRefusesWhenARemoteTipMoved(t *testing.T) {
	git := &fakeGit{current: "beta", branches: []string{"main", "alpha", "beta"},
		tips: map[string]string{"alpha": "aaa111", "beta": "bbb222"}}
	service := Service{Git: git, Selector: graphiteSelector(git, fakeGraphite{stackPaths: map[string]graphite.Stack{"beta": {Path: []string{"main", "alpha", "beta"}, Trunks: []string{"main"}}}})}
	preview, err := service.Plan(context.Background(), link.Selection{}, "origin")
	if err != nil {
		t.Fatal(err)
	}

	git.tips = map[string]string{"alpha": "aaa111", "beta": "ccc333"}
	if _, err := service.Revalidate(context.Background(), link.Selection{}, "origin", preview); err == nil {
		t.Fatal("Revalidate() error = nil after a remote tip moved")
	}
}

// A branch not yet on the remote leases the zero object id, which git reads as
// "must not exist" and rejects if one has appeared.
func TestUnpushedBranchesLeaseTheAbsentValue(t *testing.T) {
	git := &fakeGit{current: "beta", branches: []string{"main", "alpha", "beta"},
		tips: map[string]string{"alpha": "aaa111"}}
	service := Service{Git: git, Selector: graphiteSelector(git, fakeGraphite{stackPaths: map[string]graphite.Stack{"beta": {Path: []string{"main", "alpha", "beta"}, Trunks: []string{"main"}}}})}

	plan, err := service.Plan(context.Background(), link.Selection{}, "origin")
	if err != nil {
		t.Fatal(err)
	}
	for _, lease := range plan.Leases() {
		if lease.Branch == "beta" && lease.Argument() != "--force-with-lease=refs/heads/beta:0000000000000000000000000000000000000000" {
			t.Errorf("beta lease = %q", lease.Argument())
		}
	}
}

// graphiteSelector wraps a Graphite fixture as the source it now is. These
// cases assert Graphite-backed behaviour, which selection becoming pluggable
// must not have changed.
func graphiteSelector(git stack.Git, graphiteClient stack.Graphite) stack.PathSelector {
	return stack.GraphiteSelector{Git: git, Graphite: graphiteClient}
}

// ReadForest states the same shape the configured paths describe. Selection
// reads the forest, so a fake whose answers disagree tests nothing.
func (f fakeGraphite) ReadForest(context.Context) (graphite.Forest, error) {
	return forest.OfStacks(f.paths, f.stackPaths), nil
}

// changingGraphite answers differently on the second read, which is what
// revalidation exists to notice.
func (f *changingGraphite) ReadForest(context.Context) (graphite.Forest, error) {
	declared := f.first
	if f.calls > 0 {
		declared = f.next
	}
	f.calls++
	return forest.OfStacks(map[string]graphite.Stack{"middle": declared}), nil
}

// Resolve and Divergence describe a branch with work the remote does not have,
// which is the ordinary case these tests are about: something to publish, and
// nothing on the remote that publishing would overwrite.
func (f *fakeGit) Resolve(_ context.Context, rev string) (string, error) { return "object-" + rev, nil }

func (f *fakeGit) Divergence(context.Context, string, string) (int, int, error) { return 0, 1, nil }

// comparingGit answers the two reads that turn observed remote tips into a
// statement about what the push would do.
type comparingGit struct {
	fakeGit
	// local maps a branch to its tip, and known lists the objects this
	// repository has. A remote tip that is absent from known is what being
	// behind looks like before a fetch.
	local  map[string]string
	known  map[string]bool
	ours   map[string]int
	theirs map[string]int
	divErr error
}

func (g *comparingGit) Resolve(_ context.Context, rev string) (string, error) {
	if tip, ok := g.local[rev]; ok {
		return tip, nil
	}
	if g.known[rev] {
		return rev, nil
	}
	return "", fmt.Errorf("revision %q is not a commit in this repository", rev)
}

func (g *comparingGit) Divergence(_ context.Context, _, target string) (int, int, error) {
	return g.theirs[target], g.ours[target], g.divErr
}

// The preview rendered the same lines whether a branch was ahead, already
// published, or about to overwrite somebody else's commit. The last of those
// is a force-push, and the lease rejecting it is not the same as the preview
// saying so.
func TestAPlanSaysWhatPublishingEachBranchWouldDo(t *testing.T) {
	for _, test := range []struct {
		name     string
		git      *comparingGit
		want     Publication
		rejected bool
	}{
		{
			name: "work to publish",
			git: &comparingGit{
				fakeGit: fakeGit{current: "synthetic-top", tips: map[string]string{"synthetic-top": "remote-tip"}},
				local:   map[string]string{"synthetic-top": "local-tip"},
				known:   map[string]bool{"remote-tip": true},
				ours:    map[string]int{"synthetic-top": 2},
			},
			want: Publication{Ours: 2},
		},
		{
			name: "already published",
			git: &comparingGit{
				fakeGit: fakeGit{current: "synthetic-top", tips: map[string]string{"synthetic-top": "same-tip"}},
				local:   map[string]string{"synthetic-top": "same-tip"},
				known:   map[string]bool{"same-tip": true},
			},
			want: Publication{},
		},
		{
			name: "the remote has moved on",
			git: &comparingGit{
				fakeGit: fakeGit{current: "synthetic-top", tips: map[string]string{"synthetic-top": "remote-tip"}},
				local:   map[string]string{"synthetic-top": "local-tip"},
				known:   map[string]bool{"remote-tip": true},
				theirs:  map[string]int{"synthetic-top": 1},
			},
			want:     Publication{Theirs: 1},
			rejected: true,
		},
		{
			name: "the remote is on a commit we do not have",
			git: &comparingGit{
				fakeGit: fakeGit{current: "synthetic-top", tips: map[string]string{"synthetic-top": "unfetched"}},
				local:   map[string]string{"synthetic-top": "local-tip"},
			},
			want:     Publication{Unknown: true},
			rejected: true,
		},
		{
			name: "not on the remote at all",
			git: &comparingGit{
				fakeGit: fakeGit{current: "synthetic-top", tips: map[string]string{}},
				local:   map[string]string{"synthetic-top": "local-tip"},
			},
			want: Publication{New: true},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.git.fakeGit.branches = []string{"main", "synthetic-top"}
			selector := graphiteSelector(test.git, fakeGraphite{paths: map[string]graphite.Stack{
				"synthetic-top": {Path: []string{"main", "synthetic-top"}, Trunks: []string{"main"}},
			}})
			plan, err := Service{Git: test.git, Selector: selector}.Plan(context.Background(), link.Selection{}, "origin")
			if err != nil {
				t.Fatalf("Plan() error = %v", err)
			}

			if got := plan.Publishing["synthetic-top"]; got != test.want {
				t.Errorf("Publishing = %+v, want %+v", got, test.want)
			}
			if blocked := plan.Blocked != ""; blocked != test.rejected {
				t.Errorf("Blocked = %q, want rejected=%t", plan.Blocked, test.rejected)
			}
		})
	}
}

// A plan whose comparison never ran must not claim the remote already has
// everything. The zero Publication reads as "up to date", which is the most
// reassuring thing it could mean and the one thing it must not say.
func TestAnUncomparedPlanDoesNotClaimTheRemoteIsCurrent(t *testing.T) {
	plan := Plan{Snapshot: stack.Snapshot{Branches: []string{"synthetic-top"}}}

	if plan.NothingToPublish() {
		t.Error("a plan with no comparison reported the remote already has every branch")
	}
}

// Cherry reports work the base does not have, so a branch absent from the
// remote reads as new rather than as one that merged and was deleted.
func (f *fakeGit) Cherry(_ context.Context, _, head, _ string) (absent, present []string, err error) {
	return testutil.OwnCommits(head), nil, nil
}

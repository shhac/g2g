package push

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/graphite"
	"github.com/shhac/gt2gh/internal/link"
)

func TestPlanTargetsCurrentOrExplicitBranchWithoutCheckout(t *testing.T) {
	for _, test := range []struct {
		name      string
		selection link.Selection
		want      string
	}{
		{"current", link.Selection{}, "lower,middle"},
		{"explicit", link.Selection{Branch: "top"}, "lower,middle,top"},
	} {
		t.Run(test.name, func(t *testing.T) {
			git := &fakeGit{current: "middle", branches: []string{"main", "lower", "middle", "top"}}
			plan, err := Service{Git: git, Graphite: fakeGraphite{paths: paths()}}.Plan(context.Background(), test.selection, "origin")
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
	service := Service{Git: git, Graphite: fakeGraphite{paths: paths(), stackPaths: map[string]graphite.Stack{"middle": {Path: []string{"main", "lower", "middle", "top"}, Trunks: []string{"main"}}}}}
	plan, err := service.Plan(context.Background(), link.Selection{Stack: true}, "origin")
	if err != nil || strings.Join(plan.Branches, ",") != "lower,middle,top" {
		t.Fatalf("Plan() = (%#v, %v)", plan, err)
	}
	service.Graphite = fakeGraphite{paths: paths(), stackErr: errors.New("multiple descendants")}
	if _, err := service.Plan(context.Background(), link.Selection{Stack: true}, "origin"); err == nil || !strings.Contains(err.Error(), "multiple descendants") {
		t.Fatalf("Plan() fork error = %v", err)
	}
}

func TestApplyRevalidatesThenMakesOneAtomicLeasePush(t *testing.T) {
	git := &fakeGit{current: "middle", branches: []string{"main", "lower", "middle", "top"}}
	service := Service{Git: git, Graphite: fakeGraphite{paths: paths(), stackPaths: map[string]graphite.Stack{"middle": {Path: []string{"main", "lower", "middle", "top"}, Trunks: []string{"main"}}}}}
	selection := link.Selection{Stack: true}
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
	service := Service{Git: git, Graphite: graphite}
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
		{"missing remote", &fakeGit{current: "middle", branches: []string{"main", "lower", "middle"}, remoteErr: errors.New("unknown remote")}, fakeGraphite{paths: paths()}, "unknown remote"},
		{"lease rejection", &fakeGit{current: "middle", branches: []string{"main", "lower", "middle"}, pushErr: errors.New("lease rejected")}, fakeGraphite{paths: paths()}, "lease rejected"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := Service{Git: test.git, Graphite: test.graphite}
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
		Graphite: fakeGraphite{paths: paths()},
	}
	if _, err := service.Plan(context.Background(), link.Selection{}, ""); err == nil || !strings.Contains(err.Error(), "nonempty") {
		t.Fatalf("Plan() error = %v", err)
	}
}

type fakeGit struct {
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
func (f *fakeGit) PushAtomic(_ context.Context, remote string, branches []string) error {
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

package submit

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/githubstack"
	"github.com/shhac/gt2gh/internal/stack"
)

func TestApplyCreatesOnlyMissingPullsBottomToTopThenLinks(t *testing.T) {
	git := &fakeGit{}
	github := &fakeGitHub{prs: []githubstack.PullRequest{{Head: "synthetic/lower", Base: "main", State: "OPEN", Number: 11}}}
	plan := Plan{Snapshot: snapshot(), Remote: "origin", Existing: github.prs}
	spec := Spec{Version: 1, Draft: true, Pulls: []Pull{{Branch: "synthetic/lower", Title: "lower"}, {Branch: "synthetic/middle", Title: "middle", Body: "body"}, {Branch: "synthetic/top", Title: "top"}}}
	if err := (Service{Git: git, GitHub: github}).Apply(context.Background(), plan, spec); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(github.created, ","); got != "synthetic/middle<-synthetic/lower,synthetic/top<-synthetic/middle" {
		t.Errorf("created = %q", got)
	}
	if git.pushes != 1 || github.links != 1 {
		t.Errorf("pushes=%d links=%d", git.pushes, github.links)
	}
}

func TestApplyDoesNothingForInvalidOrBlockedPlan(t *testing.T) {
	for _, plan := range []Plan{{Snapshot: snapshot(), Remote: "origin", Issues: map[string]string{"synthetic/middle": "closed pull request"}}, {Snapshot: snapshot(), Remote: "origin"}} {
		git, github := &fakeGit{}, &fakeGitHub{}
		spec := Spec{Version: 1, Pulls: []Pull{{Branch: "synthetic/lower"}, {Branch: "synthetic/middle"}, {Branch: "synthetic/top"}}}
		if err := (Service{Git: git, GitHub: github}).Apply(context.Background(), plan, spec); err == nil {
			t.Fatal("Apply() = nil, want error")
		}
		if git.pushes != 0 || len(github.created) != 0 || github.links != 0 {
			t.Fatalf("mutated git=%d creates=%v links=%d", git.pushes, github.created, github.links)
		}
	}
}

func TestApplyPushFailureCreatesNothing(t *testing.T) {
	git, github := &fakeGit{pushErr: errors.New("synthetic lease rejection")}, &fakeGitHub{}
	spec := Spec{Version: 1, Pulls: []Pull{{Branch: "synthetic/lower", Title: "a"}, {Branch: "synthetic/middle", Title: "b"}, {Branch: "synthetic/top", Title: "c"}}}
	if err := (Service{Git: git, GitHub: github}).Apply(context.Background(), Plan{Snapshot: snapshot(), Remote: "origin"}, spec); err == nil {
		t.Fatal("Apply() = nil")
	}
	if len(github.created) != 0 || github.links != 0 {
		t.Fatalf("GitHub mutated: %#v %d", github.created, github.links)
	}
}

func snapshot() stack.Snapshot {
	return stack.Snapshot{Base: "main", Branches: []string{"synthetic/lower", "synthetic/middle", "synthetic/top"}}
}

type fakeGit struct {
	pushes  int
	pushErr error
}

func (*fakeGit) CurrentBranch(context.Context) (string, error) { return "synthetic/top", nil }
func (*fakeGit) LocalBranches(context.Context) ([]string, error) {
	return []string{"main", "synthetic/lower", "synthetic/middle", "synthetic/top"}, nil
}
func (*fakeGit) Clean(context.Context) error                          { return nil }
func (*fakeGit) Remote(context.Context, string) error                 { return nil }
func (f *fakeGit) PushAtomic(context.Context, string, []string) error { f.pushes++; return f.pushErr }

type fakeGitHub struct {
	prs     []githubstack.PullRequest
	created []string
	links   int
}

func (f *fakeGitHub) Inspect(context.Context, []string) ([]githubstack.PullRequest, error) {
	return f.prs, nil
}
func (f *fakeGitHub) Create(_ context.Context, branch, base, _, _ string, _ bool, _ []string) error {
	f.created = append(f.created, branch+"<-"+base)
	return nil
}
func (f *fakeGitHub) Link(context.Context, string, []string) error { f.links++; return nil }

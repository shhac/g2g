package submit

import (
	"context"
	"errors"
	"strings"
	"testing"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/push"
	"github.com/shhac/g2g/internal/stack"
	"github.com/shhac/g2g/internal/testutil"
)

func TestApplyCreatesOnlyMissingPullsBottomToTopThenLinks(t *testing.T) {
	git := &fakeGit{}
	github := &fakeGitHub{prs: []githubstack.PullRequest{{Head: "synthetic/lower", Base: "main", State: "OPEN", Number: 11}}}
	plan := Plan{Snapshot: snapshot(), Remote: "origin", Existing: github.prs}
	spec := Spec{Version: 1, Draft: true, Pulls: []Pull{{Branch: "synthetic/lower", Title: "lower"}, {Branch: "synthetic/middle", Title: "middle", Body: "body"}, {Branch: "synthetic/top", Title: "top"}}}
	if err := (Service{Git: git, GitHub: github, Pusher: git}).Apply(context.Background(), plan, spec, true); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(github.created, ","); got != "synthetic/middle<-synthetic/lower,synthetic/top<-synthetic/middle" {
		t.Errorf("created = %q", got)
	}
	if git.pushes != 1 || github.links != 1 {
		t.Errorf("pushes=%d links=%d", git.pushes, github.links)
	}
}

// A linked pull request is one GitHub will not merge through gh pr merge, so
// land could not take down a stack submit had linked. Linking is asked for.
func TestApplyLinksOnlyWhenAsked(t *testing.T) {
	git, github := &fakeGit{}, &fakeGitHub{}
	spec := Spec{Version: 1, Draft: true, Pulls: []Pull{{Branch: "synthetic/lower", Title: "lower"}, {Branch: "synthetic/middle", Title: "middle"}, {Branch: "synthetic/top", Title: "top"}}}
	if err := (Service{Git: git, GitHub: github, Pusher: git}).Apply(context.Background(), Plan{Snapshot: snapshot(), Remote: "origin"}, spec, false); err != nil {
		t.Fatal(err)
	}
	if git.pushes != 1 || len(github.created) != 3 || github.links != 0 {
		t.Errorf("pushes=%d creates=%d links=%d, want the stack published and not linked", git.pushes, len(github.created), github.links)
	}
}

func TestApplyDoesNothingForInvalidOrBlockedPlan(t *testing.T) {
	for _, plan := range []Plan{{Snapshot: snapshot(), Remote: "origin", Issues: map[string]string{"synthetic/middle": "closed pull request"}}, {Snapshot: snapshot(), Remote: "origin"}} {
		git, github := &fakeGit{}, &fakeGitHub{}
		spec := Spec{Version: 1, Pulls: []Pull{{Branch: "synthetic/lower"}, {Branch: "synthetic/middle"}, {Branch: "synthetic/top"}}}
		if err := (Service{Git: git, GitHub: github, Pusher: git}).Apply(context.Background(), plan, spec, true); err == nil {
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
	if err := (Service{Git: git, GitHub: github, Pusher: git}).Apply(context.Background(), Plan{Snapshot: snapshot(), Remote: "origin"}, spec, true); err == nil {
		t.Fatal("Apply() = nil")
	}
	if len(github.created) != 0 || github.links != 0 {
		t.Fatalf("GitHub mutated: %#v %d", github.created, github.links)
	}
}

func TestApplyStopsOnCreateFailureAndDoesNotLink(t *testing.T) {
	git := &fakeGit{}
	github := &fakeGitHub{createErrAt: 2}
	spec := Spec{Version: 1, Pulls: []Pull{{Branch: "synthetic/lower", Title: "lower"}, {Branch: "synthetic/middle", Title: "middle"}, {Branch: "synthetic/top", Title: "top"}}}
	err := (Service{Git: git, GitHub: github, Pusher: git}).Apply(context.Background(), Plan{Snapshot: snapshot(), Remote: "origin"}, spec, true)
	if err == nil || !strings.Contains(err.Error(), "synthetic create failure") {
		t.Fatalf("Apply() error = %v", err)
	}
	if got, want := strings.Join(github.created, ","), "synthetic/lower<-main,synthetic/middle<-synthetic/lower"; got != want {
		t.Errorf("created = %q, want %q", got, want)
	}
	if github.links != 0 {
		t.Errorf("links = %d, want 0", github.links)
	}
	// The push published the stack and the first pull request opened, and
	// both stand.
	var stopped *Stopped
	if !errors.As(err, &stopped) || !stopped.Pushed || strings.Join(stopped.Opened, ",") != "synthetic/lower" {
		t.Errorf("Apply() error = %#v, want a stop naming the push and synthetic/lower", err)
	}
}

// Nothing to publish and the first pull request refused means nothing
// happened, which is the ordinary failure rather than a stop part-way.
func TestApplyThatChangedNothingIsNotAStop(t *testing.T) {
	git := &fakeGit{}
	github := &fakeGitHub{createErrAt: 1}
	current := push.Publication{Standing: push.Current}
	plan := Plan{Snapshot: snapshot(), Remote: "origin", Push: push.Plan{
		Snapshot:   snapshot(),
		Publishing: map[string]push.Publication{"synthetic/lower": current, "synthetic/middle": current, "synthetic/top": current},
	}}
	spec := Spec{Version: 1, Pulls: []Pull{{Branch: "synthetic/lower", Title: "lower"}, {Branch: "synthetic/middle", Title: "middle"}, {Branch: "synthetic/top", Title: "top"}}}
	err := (Service{Git: git, GitHub: github, Pusher: git}).Apply(context.Background(), plan, spec, true)
	var stopped *Stopped
	if err == nil || errors.As(err, &stopped) {
		t.Fatalf("Apply() error = %v, want an ordinary failure", err)
	}
}

func TestApplyLinkFailureFollowsSuccessfulCreation(t *testing.T) {
	git := &fakeGit{}
	github := &fakeGitHub{linkErr: errors.New("synthetic link failure")}
	spec := Spec{Version: 1, Pulls: []Pull{{Branch: "synthetic/lower", Title: "lower"}, {Branch: "synthetic/middle", Title: "middle"}, {Branch: "synthetic/top", Title: "top"}}}
	err := (Service{Git: git, GitHub: github, Pusher: git}).Apply(context.Background(), Plan{Snapshot: snapshot(), Remote: "origin"}, spec, true)
	if err == nil || !strings.Contains(err.Error(), "synthetic link failure") {
		t.Fatalf("Apply() error = %v", err)
	}
	if git.pushes != 1 || len(github.created) != 3 || github.links != 1 {
		t.Errorf("pushes=%d creates=%d links=%d", git.pushes, len(github.created), github.links)
	}
	var stopped *Stopped
	if !errors.As(err, &stopped) || len(stopped.Opened) != 3 {
		t.Errorf("Apply() error = %#v, want a stop naming all three opened", err)
	}
}

func snapshot() stack.Snapshot {
	return stack.Snapshot{Base: "main", Branches: []string{"synthetic/lower", "synthetic/middle", "synthetic/top"}}
}

type fakeGit struct {
	published   push.Plan
	pushBlocked string
	pushes      int
	pushErr     error
	cleanErr    error
	remoteErr   error
}

func (*fakeGit) CurrentBranch(context.Context) (string, error) { return "synthetic/top", nil }
func (*fakeGit) LocalBranches(context.Context) ([]string, error) {
	return []string{"main", "synthetic/lower", "synthetic/middle", "synthetic/top"}, nil
}
func (f *fakeGit) Clean(context.Context) error          { return f.cleanErr }
func (f *fakeGit) Remote(context.Context, string) error { return f.remoteErr }

// The fake Git also stands in for push. Publishing is push's, so what these
// tests need of it is only whether it was asked to publish and what it said.
func (f *fakeGit) Plan(_ context.Context, selection stack.Selection, remote string, upstream localgit.Upstream) (push.Plan, error) {
	if f.remoteErr != nil {
		return push.Plan{}, f.remoteErr
	}
	tips := testutil.RemoteTips(snapshot().Branches)
	return push.Plan{Remote: remote, RemoteTips: tips, Upstream: upstream, Blocked: f.pushBlocked}, nil
}

func (f *fakeGit) Execute(_ context.Context, plan push.Plan) error {
	f.pushes++
	f.published = plan
	return f.pushErr
}

type fakeGitHub struct {
	prs         []githubstack.PullRequest
	later       []githubstack.PullRequest
	laterSet    bool
	inspections int
	created     []string
	links       int
	createErrAt int
	linkErr     error
}

// Inspect returns later on every call after the first when one is configured,
// which is how a revalidation observes GitHub changing under a preview.
func (f *fakeGitHub) Inspect(context.Context, []string) ([]githubstack.PullRequest, error) {
	f.inspections++
	if f.laterSet && f.inspections > 1 {
		return f.later, nil
	}
	return f.prs, nil
}
func (f *fakeGitHub) Create(_ context.Context, branch, base, _, _ string, _ bool, _ []string) error {
	f.created = append(f.created, branch+"<-"+base)
	if f.createErrAt == len(f.created) {
		return errors.New("synthetic create failure")
	}
	return nil
}
func (f *fakeGitHub) Link(context.Context, string, []string) error { f.links++; return f.linkErr }

// A reviewer reaches gh as "--reviewer <value>", so a value gh would read as
// an option has to be refused. What matters is when: the push publishes refs
// and cannot be undone, so leaving the refusal to gh meant it arrived after
// the irreversible half of the apply had already happened.
func TestAnOptionLikeReviewerIsRefusedBeforeThePush(t *testing.T) {
	git, github := &fakeGit{}, &fakeGitHub{}
	spec := Spec{Version: 1, Pulls: []Pull{
		{Branch: "synthetic/lower", Title: "a"},
		{Branch: "synthetic/middle", Title: "b", Reviewers: []string{"--synthetic-option"}},
		{Branch: "synthetic/top", Title: "c"},
	}}

	err := (Service{Git: git, GitHub: github, Pusher: git}).Apply(context.Background(), Plan{Snapshot: snapshot(), Remote: "origin"}, spec, true)

	if err == nil {
		t.Fatal("Apply() = nil for a reviewer gh would read as an option")
	}
	if !strings.Contains(err.Error(), "synthetic/middle") {
		t.Errorf("refusal does not name the branch: %v", err)
	}
	if git.pushes != 0 {
		t.Errorf("pushed %d times before refusing; the push cannot be taken back", git.pushes)
	}
	if len(github.created) != 0 || github.links != 0 {
		t.Errorf("GitHub mutated: %#v %d", github.created, github.links)
	}
}

// Publishing is push's, refusals included. A reviewer's commit on the remote
// used to be force-pushed over, because a lease pinned to the tip just read
// always matches; push refuses that, and submit now asks it.
func TestAPushThatWouldDropRemoteWorkBlocksTheSubmission(t *testing.T) {
	github := &fakeGitHub{}
	service, git := planService(github)
	git.pushBlocked = "the remote has moved on synthetic/middle"
	plan, err := service.Plan(context.Background(), stack.Selection{}, "origin", localgit.SetUpstream)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Blocked(), "synthetic/middle") {
		t.Fatalf("Blocked() = %q, want push's refusal", plan.Blocked())
	}
	spec := Spec{Version: 1, Pulls: []Pull{{Branch: "synthetic/lower", Title: "a"}, {Branch: "synthetic/middle", Title: "b"}, {Branch: "synthetic/top", Title: "c"}}}
	if err := service.Apply(context.Background(), plan, spec, true); err == nil {
		t.Fatal("Apply() published over work the remote has")
	}
	if git.pushes != 0 || len(github.created) != 0 {
		t.Errorf("mutated: pushes=%d created=%v", git.pushes, github.created)
	}
}

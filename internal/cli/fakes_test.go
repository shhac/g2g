package cli

import (
	"bytes"
	"context"
	"testing"

	"github.com/shhac/gt2gh/internal/githubstack"
	"github.com/shhac/gt2gh/internal/graphite"
	"github.com/shhac/gt2gh/internal/link"
	"github.com/shhac/gt2gh/internal/stack"
)

// Fakes and helpers shared by every test in this package. They live apart from
// the cases so a reader looking for what a command does is not wading through
// the scaffolding that lets it run offline.

func execute(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	command := New("v0.1.0", &stdout, &stderr)
	command.SetArgs(args)
	err := command.Execute()
	return stdout.String(), err
}

func executeWithService(t *testing.T, service link.Service, args ...string) (string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	command := NewWithService("v0.1.0", &stdout, &stderr, service)
	command.SetArgs(args)
	err := command.Execute()
	return stdout.String(), err
}

func cliService(github *cliGitHub) link.Service {
	return cliServiceWithGitHub(github)
}

func cliServiceWithGitHub(github link.GitHub) link.Service {
	git := cliGit{current: "beta", branches: []string{"main", "alpha", "beta"}}
	return link.Service{
		Git:      git,
		Selector: stack.GraphiteSelector{Git: git, Graphite: cliGraphite{}},
		GitHub:   github,
	}
}

func cliSingleBranchService(github link.GitHub) link.Service {
	git := cliGit{current: "alpha", branches: []string{"main", "alpha"}}
	return link.Service{
		Git:      git,
		Selector: stack.GraphiteSelector{Git: git, Graphite: cliSingleBranchGraphite{}},
		GitHub:   github,
	}
}

type cliGit struct {
	current  string
	branches []string
}

type cliGraphite struct{}

func (cliGraphite) Discover(_ context.Context, branch string) (graphite.Stack, error) {
	if branch != "beta" {
		return graphite.Stack{}, context.Canceled
	}
	return graphite.Stack{Path: []string{"main", "alpha", "beta"}, Trunks: []string{"main"}}, nil
}

type cliSingleBranchGraphite struct{}

func (cliSingleBranchGraphite) Discover(_ context.Context, branch string) (graphite.Stack, error) {
	if branch != "alpha" {
		return graphite.Stack{}, context.Canceled
	}
	return graphite.Stack{Path: []string{"main", "alpha"}, Trunks: []string{"main"}}, nil
}

type cliGitHub struct {
	links        int
	base         string
	branches     []string
	events       *[]string
	linkErr      error
	inspectErrAt int
	inspectErr   error
	inspectCalls int
}

type recordingWriter struct {
	bytes.Buffer
	events   *[]string
	flushErr error
	writeErr error
}

func eventIndex(events []string, want string) int {
	for index, event := range events {
		if event == want {
			return index
		}
	}
	return -1
}

func eventIndexes(events []string, want string) []int {
	var indexes []int
	for index, event := range events {
		if event == want {
			indexes = append(indexes, index)
		}
	}
	return indexes
}

type cliGitHubMissing struct{ links int }

func (*cliGitHubMissing) Inspect(context.Context, []string) ([]githubstack.PullRequest, error) {
	return []githubstack.PullRequest{{Number: 1, Head: "alpha", Base: "main", State: "OPEN"}}, nil
}

type cliGitHubPRs struct {
	prs   []githubstack.PullRequest
	links int
}

func (f cliGit) CurrentBranch(context.Context) (string, error)   { return f.current, nil }
func (f cliGit) LocalBranches(context.Context) ([]string, error) { return f.branches, nil }
func (cliGit) Clean(context.Context) error                       { return nil }

func (f cliGraphite) DiscoverStack(ctx context.Context, branch string, _ bool) (graphite.Stack, error) {
	return f.Discover(ctx, branch)
}

func (cliGraphite) TrackedBranches(context.Context) ([]string, error) {
	return []string{"alpha", "beta"}, nil
}

func (f cliSingleBranchGraphite) DiscoverStack(ctx context.Context, branch string, _ bool) (graphite.Stack, error) {
	return f.Discover(ctx, branch)
}

func (cliSingleBranchGraphite) TrackedBranches(context.Context) ([]string, error) {
	return []string{"alpha"}, nil
}

func (f *cliGitHub) Inspect(_ context.Context, branches []string) ([]githubstack.PullRequest, error) {
	f.inspectCalls++
	if f.events != nil {
		*f.events = append(*f.events, "inspect")
	}
	if f.inspectErr != nil && f.inspectCalls == f.inspectErrAt {
		return nil, f.inspectErr
	}
	prs := make([]githubstack.PullRequest, 0, len(branches))
	base := "main"
	for index, branch := range branches {
		prs = append(prs, githubstack.PullRequest{Number: index + 1, Head: branch, Base: base, State: "OPEN"})
		base = branch
	}
	return prs, nil
}

func (f *cliGitHub) Link(_ context.Context, base string, branches []string) error {
	f.links++
	f.base = base
	f.branches = append([]string(nil), branches...)
	if f.events != nil {
		*f.events = append(*f.events, "link")
	}
	return f.linkErr
}

func (w *recordingWriter) Write(data []byte) (int, error) {
	if w.events != nil {
		*w.events = append(*w.events, "write")
	}
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	return w.Buffer.Write(data)
}

func (w *recordingWriter) Flush() error {
	if w.events != nil {
		*w.events = append(*w.events, "flush")
	}
	return w.flushErr
}

func (f *cliGitHubMissing) Link(context.Context, string, []string) error { f.links++; return nil }

func (f *cliGitHubPRs) Inspect(context.Context, []string) ([]githubstack.PullRequest, error) {
	return f.prs, nil
}

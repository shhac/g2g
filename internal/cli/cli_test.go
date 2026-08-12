package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/githubstack"
	"github.com/shhac/gt2gh/internal/graphite"
	"github.com/shhac/gt2gh/internal/link"
	syncer "github.com/shhac/gt2gh/internal/sync"
)

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

func executeWithServices(t *testing.T, linkService link.Service, syncService syncer.Service, args ...string) (string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	command := NewWithServices("v0.2.0", &stdout, &stderr, linkService, syncService)
	command.SetArgs(args)
	err := command.Execute()
	return stdout.String(), err
}

func TestBareCommandShowsHelp(t *testing.T) {
	output, err := execute(t)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(output, "  link") || !strings.Contains(output, "  sync") {
		t.Errorf("help = %q", output)
	}
}

func TestVersion(t *testing.T) {
	output, err := execute(t, "--version")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output != "gt2gh version v0.1.0\n" {
		t.Errorf("version = %q", output)
	}
}

func TestCompletionScripts(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			output, err := execute(t, "completion", shell)
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if !strings.Contains(output, "gt2gh") {
				t.Errorf("completion script does not name command")
			}
		})
	}
}

func TestCompletionRejectsUnknownShell(t *testing.T) {
	if _, err := execute(t, "completion", "powershell"); err == nil {
		t.Fatal("Execute() error = nil, want error")
	}
}

func TestLinkPreviewPrintsResolvedTargetWithoutMutation(t *testing.T) {
	github := &cliGitHub{}
	output, err := executeWithService(t, cliService(github), "link", "--branch", "beta")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, expected := range []string{
		"Resolved target (--branch): beta",
		"Declared Graphite trunk: main",
		"Graphite path (bottom to top): alpha -> beta",
		"Proposed command: gh stack link --base main alpha beta",
		"Preview only",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("preview missing %q:\n%s", expected, output)
		}
	}
	if github.links != 0 {
		t.Errorf("Link calls = %d, want 0", github.links)
	}
}

func TestBareLinkPreviewPrintsCurrentTargetWithoutMutation(t *testing.T) {
	github := &cliGitHub{}
	output, err := executeWithService(t, cliService(github), "link")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(output, "Resolved target (current Git branch): beta") {
		t.Errorf("output = %q", output)
	}
	if github.links != 0 {
		t.Errorf("Link calls = %d, want 0", github.links)
	}
}

func TestLinkApplyRevalidatesThenMutates(t *testing.T) {
	github := &cliGitHub{}
	output, err := executeWithService(t, cliService(github), "link", "--apply")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(output, "Applied: gh stack link completed after revalidation.") {
		t.Errorf("output = %q", output)
	}
	if github.links != 1 {
		t.Errorf("Link calls = %d, want 1", github.links)
	}
}

func TestBranchCompletionUsesTrackedLocalBranchNames(t *testing.T) {
	output, err := executeWithService(t, cliService(&cliGitHub{}), "__complete", "link", "--branch", "be")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(output, "beta\n") || strings.Contains(output, "alpha\n") {
		t.Errorf("completion = %q", output)
	}
}

func TestSyncPreviewShowsDivergenceWithoutMutation(t *testing.T) {
	github := &cliSyncGitHub{}
	service := syncer.Service{
		Discoverer: cliSyncDiscoverer{},
		Git:        cliSyncGit{},
		GitHub:     github,
	}
	output, err := executeWithServices(t, cliService(&cliGitHub{}), service, "sync")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, expected := range []string{"Resolved target (current Git branch): beta", "beta: divergent (PR #2 base main, want alpha)", "Preview only"} {
		if !strings.Contains(output, expected) {
			t.Errorf("preview missing %q:\n%s", expected, output)
		}
	}
	if github.links != 0 {
		t.Errorf("Link calls = %d, want 0", github.links)
	}
}

func TestSyncApplyPassesExplicitBranchAndMutatesOnce(t *testing.T) {
	discoverer := &cliApplyDiscoverer{}
	github := &cliSyncGitHub{}
	service := syncer.Service{Discoverer: discoverer, Git: cliSyncGit{}, GitHub: github}
	output, err := executeWithServices(t, cliService(&cliGitHub{}), service, "sync", "--branch", "beta", "--apply")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if discoverer.branch != "beta" {
		t.Errorf("selected branch = %q, want beta", discoverer.branch)
	}
	if github.links != 1 {
		t.Errorf("Link calls = %d, want 1", github.links)
	}
	if !strings.Contains(output, "Applied: GitHub native stack reconciliation completed after revalidation.") {
		t.Errorf("output = %q", output)
	}
}

func cliService(github *cliGitHub) link.Service {
	return link.Service{
		Git:      cliGit{current: "beta", branches: []string{"main", "alpha", "beta"}},
		Graphite: cliGraphite{},
		GitHub:   github,
	}
}

type cliGit struct {
	current  string
	branches []string
}

func (f cliGit) CurrentBranch(context.Context) (string, error)   { return f.current, nil }
func (f cliGit) LocalBranches(context.Context) ([]string, error) { return f.branches, nil }
func (cliGit) Clean(context.Context) error                       { return nil }

type cliGraphite struct{}

func (cliGraphite) Discover(_ context.Context, branch string) (graphite.Stack, error) {
	if branch != "beta" {
		return graphite.Stack{}, context.Canceled
	}
	return graphite.Stack{Trunk: "main", Branches: []string{"alpha", "beta"}}, nil
}
func (cliGraphite) TrackedBranches(context.Context) ([]string, error) {
	return []string{"alpha", "beta"}, nil
}

type cliGitHub struct{ links int }

func (*cliGitHub) Inspect(context.Context, []string) ([]githubstack.PullRequest, error) {
	return nil, nil
}
func (f *cliGitHub) Link(context.Context, string, []string) error {
	f.links++
	return nil
}

type cliSyncDiscoverer struct{}

func (cliSyncDiscoverer) Discover(context.Context, string) (link.Plan, error) {
	return link.Plan{
		Target:       "beta",
		TargetSource: "current Git branch",
		Trunk:        "main",
		Branches:     []string{"alpha", "beta"},
		PullRequests: []githubstack.PullRequest{{Number: 1, Head: "alpha", Base: "main", State: "OPEN"}, {Number: 2, Head: "beta", Base: "main", State: "OPEN"}},
	}, nil
}

type cliApplyDiscoverer struct{ branch string }

func (f *cliApplyDiscoverer) Discover(_ context.Context, branch string) (link.Plan, error) {
	f.branch = branch
	return link.Plan{
		Target: "beta", TargetSource: "--branch", Trunk: "main", Branches: []string{"alpha", "beta"},
		PullRequests: []githubstack.PullRequest{{Number: 1, Head: "alpha", Base: "main", State: "OPEN"}, {Number: 2, Head: "beta", Base: "alpha", State: "OPEN"}},
	}, nil
}

type cliSyncGit struct{}

func (cliSyncGit) Clean(context.Context) error { return nil }

type cliSyncGitHub struct{ links int }

func (f *cliSyncGitHub) Link(context.Context, string, []string) error {
	f.links++
	return nil
}

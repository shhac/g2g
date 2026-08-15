package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/githubstack"
	"github.com/shhac/gt2gh/internal/link"
	"github.com/shhac/gt2gh/internal/stack"
	syncer "github.com/shhac/gt2gh/internal/sync"
)

func executeWithServices(t *testing.T, linkService link.Service, syncService syncer.Service, args ...string) (string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	command := NewWithServices("v0.2.0", &stdout, &stderr, linkService, syncService)
	command.SetArgs(args)
	err := command.Execute()
	return stdout.String(), err
}

func TestSyncPreviewShowsDivergenceWithoutMutation(t *testing.T) {
	github := &cliSyncGitHub{}
	service := syncer.Service{Discoverer: cliSyncDiscoverer{}, Git: cliSyncGit{}, GitHub: github}
	output, err := executeWithServices(t, cliService(&cliGitHub{}), service, "sync")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, expected := range []string{"Target  beta", "○ main", "#1", "aligned", "#2", "divergent: base main, want alpha", "Command to run\ngh stack link --base main alpha beta", "No changes were made."} {
		if !strings.Contains(output, expected) {
			t.Errorf("preview missing %q:\n%s", expected, output)
		}
	}
	if github.links != 0 {
		t.Errorf("Link calls = %d, want 0", github.links)
	}
}

func TestSyncPlanSnapshotsUseOneSpacedGraphAndCopyableCommand(t *testing.T) {
	plan := syncer.Plan{Link: link.Plan{Discovery: stack.Discovery{Snapshot: stack.Snapshot{Target: "synthetic-top", Base: "synthetic-main", Branches: []string{"synthetic-a", "synthetic-b"}}}}, Items: []syncer.Item{{Branch: "synthetic-a", ExpectedBase: "synthetic-main", State: syncer.Aligned, PullRequest: &githubstack.PullRequest{Number: 10}}, {Branch: "synthetic-b", ExpectedBase: "synthetic-a", State: syncer.Divergent, PullRequest: &githubstack.PullRequest{Number: 11, Base: "synthetic-main"}}}}
	for _, test := range []struct {
		name         string
		presentation Presentation
	}{
		{name: "sync-plain"},
		{name: "sync-color", presentation: Presentation{Color: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			if err := writeSyncPlan(&output, plan, test.presentation); err != nil {
				t.Fatal(err)
			}
			assertGolden(t, test.name, output.String())
		})
	}
}

func TestSyncPreviewShowsMissingAndUnsafeNodesWithoutApplyCommandMutation(t *testing.T) {
	plan := syncer.Plan{Link: link.Plan{Discovery: stack.Discovery{Snapshot: stack.Snapshot{Target: "synthetic-top", Base: "synthetic-main", Branches: []string{"synthetic-a", "synthetic-b"}}}}, Items: []syncer.Item{{Branch: "synthetic-a", ExpectedBase: "synthetic-main", State: syncer.Missing}, {Branch: "synthetic-b", ExpectedBase: "synthetic-a", State: syncer.Unsafe, PullRequest: &githubstack.PullRequest{Number: 12, State: "CLOSED"}}}}
	var output bytes.Buffer
	if err := writeSyncPlan(&output, plan, Presentation{}); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"missing pull request", "#12", "non-open pull request", "Apply blocked"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("preview missing %q: %q", expected, output.String())
		}
	}
}

func TestSyncApplyPassesExplicitBranchAndMutatesOnce(t *testing.T) {
	discoverer := &cliApplyDiscoverer{}
	github := &cliSyncGitHub{}
	output, err := executeWithServices(t, cliService(&cliGitHub{}), syncer.Service{Discoverer: discoverer, Git: cliSyncGit{}, GitHub: github}, "sync", "--branch", "beta", "--apply")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if discoverer.branch != "beta" {
		t.Errorf("selected branch = %q, want beta", discoverer.branch)
	}
	if github.links != 1 {
		t.Errorf("Link calls = %d, want 1", github.links)
	}
	if !strings.Contains(output, "Ready to apply") || !strings.Contains(output, "Applied — GitHub stack updated") || !strings.Contains(output, "Changes were made.") {
		t.Errorf("output = %q", output)
	}
}

func TestSyncApplyFailureNeverReportsSuccess(t *testing.T) {
	discoverer := &cliApplyDiscoverer{}
	github := &cliSyncGitHub{err: errors.New("synthetic sync failure")}
	output, err := executeWithServices(t, cliService(&cliGitHub{}), syncer.Service{Discoverer: discoverer, Git: cliSyncGit{}, GitHub: github}, "sync", "--apply")
	if err == nil {
		t.Fatal("Execute() error = nil")
	}
	if !strings.Contains(output, "Ready to apply") || !strings.Contains(output, "Not applied\nsynthetic sync failure") || strings.Contains(output, "Applied —") || strings.Contains(output, "Changes were made.") {
		t.Errorf("output = %q", output)
	}
}

type cliSyncDiscoverer struct{}

func (cliSyncDiscoverer) DiscoverWithOptions(context.Context, link.Selection) (link.Plan, error) {
	return link.Plan{Discovery: stack.Discovery{Snapshot: stack.Snapshot{Target: "beta", TargetSource: "current Git branch", Base: "main", BaseSource: "Graphite-declared ancestry", GraphitePath: []string{"main", "alpha", "beta"}, Branches: []string{"alpha", "beta"}}, PullRequests: []githubstack.PullRequest{{Number: 1, Head: "alpha", Base: "main", State: "OPEN"}, {Number: 2, Head: "beta", Base: "main", State: "OPEN"}}}}, nil
}

type cliApplyDiscoverer struct{ branch string }

func (f *cliApplyDiscoverer) DiscoverWithOptions(_ context.Context, selection link.Selection) (link.Plan, error) {
	f.branch = selection.Branch
	return link.Plan{Discovery: stack.Discovery{Snapshot: stack.Snapshot{Target: "beta", TargetSource: "--branch", Base: "main", BaseSource: "Graphite-declared ancestry", GraphitePath: []string{"main", "alpha", "beta"}, Branches: []string{"alpha", "beta"}}, PullRequests: []githubstack.PullRequest{{Number: 1, Head: "alpha", Base: "main", State: "OPEN"}, {Number: 2, Head: "beta", Base: "alpha", State: "OPEN"}}}}, nil
}

type cliSyncGit struct{}

func (cliSyncGit) Clean(context.Context) error { return nil }

type cliSyncGitHub struct {
	links int
	err   error
}

func (f *cliSyncGitHub) Link(context.Context, string, []string) error {
	if f.err != nil {
		return f.err
	}
	f.links++
	return nil
}

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

func TestNamedExecutableGeneratesMatchingZshCompletion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	command := NewNamed("v0.2.1", "g2g", &stdout, &stderr)
	command.SetArgs([]string{"completion", "zsh"})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "#compdef g2g") {
		t.Errorf("zsh completion = %q", stdout.String())
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
		"main (trunk)", "alpha (#1)", "beta (#2)",
		"Command to run\ngh stack link --base main alpha beta",
		"No changes were made.",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("preview missing %q:\n%s", expected, output)
		}
	}
	if github.links != 0 {
		t.Errorf("Link calls = %d, want 0", github.links)
	}
}

func TestLinkPreviewGraphIsColoredOnlyWhenEnabled(t *testing.T) {
	plan := link.Plan{Target: "beta", TargetSource: "--branch", Base: "main", Branches: []string{"alpha", "beta"}, PullRequests: []githubstack.PullRequest{{Number: 1, Head: "alpha"}, {Number: 2, Head: "beta"}}}
	for _, test := range []struct {
		color bool
		want  string
	}{{false, "  main (trunk)\n  └─ alpha (#1)\n    └─ beta (#2)"}, {true, "\x1b[1;33mmain (trunk)\x1b[0m"}} {
		var output bytes.Buffer
		writePreview(&output, plan, Presentation{Color: test.color})
		if !strings.Contains(output.String(), test.want) || strings.Count(output.String(), "main (trunk)") != 1 || strings.Count(output.String(), "gh stack link --base main alpha beta") != 1 {
			t.Errorf("preview = %q", output.String())
		}
	}
}

func TestLinkPreviewCommandLineIsBareAndHighlightedOnlyWithColor(t *testing.T) {
	plan := link.Plan{Target: "beta", TargetSource: "--branch", Base: "main", Branches: []string{"beta"}, PullRequests: []githubstack.PullRequest{{Number: 1, Head: "beta"}}}
	for _, test := range []struct {
		name  string
		color bool
	}{
		{name: "plain", color: false},
		{name: "color", color: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			presentation := Presentation{Color: test.color}
			writePreview(&output, plan, presentation)
			want := presentation.accent("Command to run") + "\n" + presentation.command("gh stack link --base main beta") + "\n"
			if got := output.String(); !strings.Contains(got, want) || strings.Contains(got, "$ gh stack link") || strings.Contains(got, "│gh stack") {
				t.Errorf("preview = %q", got)
			}
		})
	}
}

func TestLinkPreviewCommandUsesShellSafeArguments(t *testing.T) {
	plan := link.Plan{Target: "feature;synthetic", TargetSource: "--branch", Base: "main", Branches: []string{"feature;synthetic"}, PullRequests: []githubstack.PullRequest{{Number: 1, Head: "feature;synthetic"}}}
	var output bytes.Buffer
	writePreview(&output, plan, Presentation{})
	if !strings.Contains(output.String(), "gh stack link --base main 'feature;synthetic'") {
		t.Errorf("preview = %q", output.String())
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

func TestLinkPreviewShowsUnresolvedNodeAndBlocksApply(t *testing.T) {
	github := &cliGitHubMissing{}
	output, err := executeWithService(t, cliServiceWithGitHub(github), "link")
	if err != nil {
		t.Fatalf("preview error = %v", err)
	}
	if !strings.Contains(output, "beta (unresolved: no open pull request)") || !strings.Contains(output, "Apply blocked") || github.links != 0 {
		t.Errorf("preview = %q links=%d", output, github.links)
	}
	_, err = executeWithService(t, cliServiceWithGitHub(github), "link", "--apply")
	if err == nil || github.links != 0 {
		t.Errorf("apply error=%v links=%d", err, github.links)
	}
}

func TestLinkPreviewLabelsEveryUnresolvedNode(t *testing.T) {
	plan := link.Plan{
		Target: "beta-two", TargetSource: "--branch", Base: "main",
		Branches:     []string{"alpha", "beta", "beta-two"},
		PullRequests: []githubstack.PullRequest{{Number: 1, Head: "alpha"}},
		Issues: []link.Issue{
			{Branch: "beta", Reason: "closed pull request"},
			{Branch: "beta-two", Reason: "no open pull request"},
		},
	}
	var output bytes.Buffer
	writePreview(&output, plan, Presentation{})
	if got := output.String(); !strings.Contains(got, "beta (unresolved: closed pull request)") || !strings.Contains(got, "beta-two (unresolved: no open pull request)") || strings.Contains(got, "(#0)") {
		t.Errorf("preview = %q", got)
	}
}

func TestLinkApplyRevalidatesThenMutates(t *testing.T) {
	github := &cliGitHub{}
	output, err := executeWithService(t, cliService(github), "link", "--apply")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(output, "Applied — GitHub stack updated") || !strings.Contains(output, "Changes were made.") {
		t.Errorf("output = %q", output)
	}
	if github.links != 1 {
		t.Errorf("Link calls = %d, want 1", github.links)
	}
}

func TestLinkApplyRendersAndFlushesValidatedPlanBeforeMutation(t *testing.T) {
	events := []string{}
	writer := &recordingWriter{events: &events}
	github := &cliGitHub{events: &events}
	command := newWithPresentation("v0.2.4", "gt2gh", writer, writer, cliService(github), syncer.Service{}, Presentation{})
	command.SetArgs([]string{"link", "--apply"})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	flush, link := eventIndex(events, "flush"), eventIndex(events, "link")
	if flush < 0 || link < 0 || flush > link {
		t.Errorf("events = %v, want flush before link", events)
	}
	output := writer.String()
	if strings.Count(output, "main (trunk)") != 1 || strings.Count(output, "gh stack link --base main alpha beta") != 1 || !strings.Contains(output, "Ready to apply") || !strings.Contains(output, "Applied — GitHub stack updated") {
		t.Errorf("output = %q", output)
	}
}

func TestLinkApplyReportsCancellationWithoutSuccess(t *testing.T) {
	events := []string{}
	writer := &recordingWriter{events: &events}
	github := &cliGitHub{events: &events, linkErr: context.Canceled}
	command := newWithPresentation("v0.2.4", "gt2gh", writer, writer, cliService(github), syncer.Service{}, Presentation{})
	command.SetArgs([]string{"link", "--apply"})
	err := command.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil")
	}
	output := writer.String()
	if !strings.Contains(output, "Ready to apply") || !strings.Contains(output, "Not applied: context canceled") || strings.Contains(output, "Applied —") || strings.Contains(output, "Changes were made.") {
		t.Errorf("output = %q", output)
	}
	if flush, link := eventIndex(events, "flush"), eventIndex(events, "link"); flush < 0 || link < 0 || flush > link {
		t.Errorf("events = %v, want flush before link", events)
	}
}

func TestLinkApplyDoesNotMutateWhenReadyOutputCannotFlush(t *testing.T) {
	events := []string{}
	writer := &recordingWriter{events: &events, flushErr: context.Canceled}
	github := &cliGitHub{events: &events}
	command := newWithPresentation("v0.2.4", "gt2gh", writer, writer, cliService(github), syncer.Service{}, Presentation{})
	command.SetArgs([]string{"link", "--apply"})
	if err := command.Execute(); err == nil {
		t.Fatal("Execute() error = nil")
	}
	if eventIndex(events, "link") >= 0 || !strings.Contains(writer.String(), "Not applied: flush ready-to-apply output") {
		t.Errorf("events = %v output = %q", events, writer.String())
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

func TestTrunkCompletionUsesDeclaredLocalTrunks(t *testing.T) {
	output, err := executeWithService(t, cliService(&cliGitHub{}), "__complete", "link", "--trunk", "m")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(output, "main\n") {
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
	for _, expected := range []string{"Resolved target (current Git branch): beta", "beta: divergent (PR #2 base main, want alpha)", "No changes were made."} {
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
	return cliServiceWithGitHub(github)
}
func cliServiceWithGitHub(github link.GitHub) link.Service {
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
	return graphite.Stack{Path: []string{"main", "alpha", "beta"}, Trunks: []string{"main"}}, nil
}
func (cliGraphite) TrackedBranches(context.Context) ([]string, error) {
	return []string{"alpha", "beta"}, nil
}

type cliGitHub struct {
	links   int
	events  *[]string
	linkErr error
}

func (*cliGitHub) Inspect(_ context.Context, branches []string) ([]githubstack.PullRequest, error) {
	prs := make([]githubstack.PullRequest, 0, len(branches))
	base := "main"
	for index, branch := range branches {
		prs = append(prs, githubstack.PullRequest{Number: index + 1, Head: branch, Base: base, State: "OPEN"})
		base = branch
	}
	return prs, nil
}
func (f *cliGitHub) Link(context.Context, string, []string) error {
	f.links++
	if f.events != nil {
		*f.events = append(*f.events, "link")
	}
	return f.linkErr
}

type recordingWriter struct {
	bytes.Buffer
	events   *[]string
	flushErr error
}

func (w *recordingWriter) Write(data []byte) (int, error) {
	if w.events != nil {
		*w.events = append(*w.events, "write")
	}
	return w.Buffer.Write(data)
}
func (w *recordingWriter) Flush() error {
	if w.events != nil {
		*w.events = append(*w.events, "flush")
	}
	return w.flushErr
}

func eventIndex(events []string, want string) int {
	for index, event := range events {
		if event == want {
			return index
		}
	}
	return -1
}

type cliGitHubMissing struct{ links int }

func (*cliGitHubMissing) Inspect(context.Context, []string) ([]githubstack.PullRequest, error) {
	return []githubstack.PullRequest{{Number: 1, Head: "alpha", Base: "main", State: "OPEN"}}, nil
}
func (f *cliGitHubMissing) Link(context.Context, string, []string) error { f.links++; return nil }

type cliSyncDiscoverer struct{}

func (cliSyncDiscoverer) DiscoverWithTrunk(context.Context, string, string) (link.Plan, error) {
	return link.Plan{
		Target:       "beta",
		TargetSource: "current Git branch",
		Base:         "main",
		BaseSource:   "Graphite-declared ancestry",
		GraphitePath: []string{"main", "alpha", "beta"},
		Branches:     []string{"alpha", "beta"},
		PullRequests: []githubstack.PullRequest{{Number: 1, Head: "alpha", Base: "main", State: "OPEN"}, {Number: 2, Head: "beta", Base: "main", State: "OPEN"}},
	}, nil
}

type cliApplyDiscoverer struct{ branch string }

func (f *cliApplyDiscoverer) DiscoverWithTrunk(_ context.Context, branch, trunk string) (link.Plan, error) {
	f.branch = branch
	return link.Plan{
		Target: "beta", TargetSource: "--branch", Base: "main", BaseSource: "Graphite-declared ancestry", GraphitePath: []string{"main", "alpha", "beta"}, Branches: []string{"alpha", "beta"},
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

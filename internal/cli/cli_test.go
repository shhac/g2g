package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/githubstack"
	"github.com/shhac/gt2gh/internal/graphite"
	"github.com/shhac/gt2gh/internal/link"
	"github.com/shhac/gt2gh/internal/push"
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
	if !strings.Contains(output, "  link") || !strings.Contains(output, "  sync") || !strings.Contains(output, "  push") {
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

func TestDebugIsPersistentStderrOnlyAndDoesNotChangeLinkMutation(t *testing.T) {
	for _, args := range [][]string{
		{"--debug", "link", "--branch", "beta"},
		{"link", "--debug", "--branch", "beta"},
	} {
		var stdout, stderr bytes.Buffer
		github := &cliGitHub{}
		command := NewWithService("v0.4.0", &stdout, &stderr, cliService(github))
		command.SetArgs(args)
		if err := command.Execute(); err != nil {
			t.Fatalf("Execute(%v) error = %v", args, err)
		}
		if github.links != 0 || strings.Contains(stdout.String(), "debug event=") {
			t.Errorf("args=%v stdout=%q links=%d", args, stdout.String(), github.links)
		}
		for _, expected := range []string{"event=operation.start", "operation=\"link\"", "target_source=\"--branch\"", "event=link.target", "event=link.trunk", "event=github.native_stack_membership", "event=link.plan", "decision=\"ready\""} {
			if !strings.Contains(stderr.String(), expected) {
				t.Errorf("args=%v debug missing %q: %q", args, expected, stderr.String())
			}
		}
	}
}

func TestNormalLinkLeavesStderrQuiet(t *testing.T) {
	var stdout, stderr bytes.Buffer
	command := NewWithService("v0.4.0", &stdout, &stderr, cliService(&cliGitHub{}))
	command.SetArgs([]string{"link"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := stderr.String(); got != "" {
		t.Errorf("stderr = %q, want empty without --debug", got)
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
		"Target: beta",
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
		if err := writeLinkPlan(&output, plan, Presentation{Color: test.color}); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), test.want) || strings.Count(output.String(), "main (trunk)") != 1 || strings.Count(output.String(), "gh stack link --base main alpha beta") != 1 {
			t.Errorf("preview = %q", output.String())
		}
	}
}

func TestLinkPlanSnapshots(t *testing.T) {
	resolved := link.Plan{
		Target: "beta", TargetSource: "--branch", Base: "main", Branches: []string{"alpha", "beta"},
		PullRequests: []githubstack.PullRequest{{Number: 1, Head: "alpha"}, {Number: 2, Head: "beta"}},
	}
	unresolved := resolved
	unresolved.Issues = []link.Issue{{Branch: "beta", Reason: "no open pull request"}}
	for _, test := range []struct {
		name         string
		plan         link.Plan
		presentation Presentation
		want         string
	}{
		{
			name: "plain resolved", plan: resolved,
			want: "Target: beta\n\n  main (trunk)\n  └─ alpha (#1)\n    └─ beta (#2)\n\nCommand to run\ngh stack link --base main alpha beta\n",
		},
		{
			name: "color resolved", plan: resolved, presentation: Presentation{Color: true},
			want: "\x1b[1;36mTarget\x1b[0m: \x1b[1;37mbeta\x1b[0m\n\n  \x1b[1;33mmain (trunk)\x1b[0m\n  └─ \x1b[1;37malpha\x1b[0m (\x1b[35m#1\x1b[0m)\n    └─ \x1b[1;37mbeta\x1b[0m (\x1b[35m#2\x1b[0m)\n\n\x1b[1;36mCommand to run\x1b[0m\n\x1b[1;97;48;5;236mgh stack link --base main alpha beta\x1b[0m\n",
		},
		{
			name: "plain unresolved", plan: unresolved,
			want: "Target: beta\n\n  main (trunk)\n  └─ alpha (#1)\n    └─ beta (unresolved: no open pull request)\n\nCommand to run\ngh stack link --base main alpha beta\nApply blocked: resolve every unresolved GitHub PR mapping first.\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			if err := writeLinkPlan(&output, test.plan, test.presentation); err != nil {
				t.Fatal(err)
			}
			if got := output.String(); got != test.want {
				t.Errorf("snapshot = %q, want %q", got, test.want)
			}
			if strings.Contains(output.String(), "Link stack") || strings.Contains(output.String(), "bottom to top") || strings.Contains(output.String(), "current Git branch") {
				t.Errorf("snapshot has redundant leader: %q", output.String())
			}
		})
	}
}

func TestLinkPreviewCommandLineIsBareAndHighlightedOnlyWithColor(t *testing.T) {
	plan := link.Plan{Target: "beta", TargetSource: "--branch", Base: "main", Branches: []string{"alpha", "beta"}, PullRequests: []githubstack.PullRequest{{Number: 1, Head: "alpha"}, {Number: 2, Head: "beta"}}}
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
			if err := writeLinkPlan(&output, plan, presentation); err != nil {
				t.Fatal(err)
			}
			want := presentation.accent("Command to run") + "\n" + presentation.command("gh stack link --base main alpha beta") + "\n"
			if got := output.String(); !strings.Contains(got, want) || strings.Contains(got, "$ gh stack link") || strings.Contains(got, "│gh stack") {
				t.Errorf("preview = %q", got)
			}
		})
	}
}

func TestLinkPreviewCommandUsesShellSafeArguments(t *testing.T) {
	plan := link.Plan{Target: "feature;synthetic", TargetSource: "--branch", Base: "main", Branches: []string{"alpha", "feature;synthetic"}, PullRequests: []githubstack.PullRequest{{Number: 1, Head: "alpha"}, {Number: 2, Head: "feature;synthetic"}}}
	var output bytes.Buffer
	if err := writeLinkPlan(&output, plan, Presentation{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "gh stack link --base main alpha 'feature;synthetic'") {
		t.Errorf("preview = %q", output.String())
	}
}

func TestLinkPreviewReportsNothingToLinkForOnePullRequest(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "current branch", args: []string{"link"}, want: "Target: alpha"},
		{name: "explicit branch", args: []string{"link", "--branch", "alpha"}, want: "Target: alpha"},
	} {
		t.Run(test.name, func(t *testing.T) {
			github := &cliGitHub{}
			output, err := executeWithService(t, cliSingleBranchService(github), test.args...)
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			for _, expected := range []string{test.want, "Nothing to link — this stack has one pull request.", "No changes were needed or made."} {
				if !strings.Contains(output, expected) {
					t.Errorf("preview missing %q:\n%s", expected, output)
				}
			}
			if strings.Contains(output, "gh stack link") || github.links != 0 {
				t.Errorf("preview = %q links=%d", output, github.links)
			}
		})
	}
}

func TestBareLinkPreviewPrintsCurrentTargetWithoutMutation(t *testing.T) {
	github := &cliGitHub{}
	output, err := executeWithService(t, cliService(github), "link")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(output, "Target: beta") {
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

func TestLinkOneBranchUnresolvedStateIsNotNothingToLink(t *testing.T) {
	for _, test := range []struct {
		name string
		prs  []githubstack.PullRequest
		want string
	}{
		{name: "missing", want: "alpha (unresolved: no open pull request)"},
		{name: "non-open", prs: []githubstack.PullRequest{{Number: 1, Head: "alpha", Base: "main", State: "CLOSED"}}, want: "alpha (unresolved: closed pull request)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			github := &cliGitHubPRs{prs: test.prs}
			service := cliSingleBranchService(github)
			output, err := executeWithService(t, service, "link")
			if err != nil {
				t.Fatalf("preview error = %v", err)
			}
			if !strings.Contains(output, test.want) || !strings.Contains(output, "Apply blocked") || strings.Contains(output, "Nothing to link") {
				t.Errorf("preview = %q", output)
			}
			if _, err := executeWithService(t, service, "link", "--apply"); err == nil || github.links != 0 {
				t.Errorf("apply error=%v links=%d", err, github.links)
			}
		})
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
	if err := writeLinkPlan(&output, plan, Presentation{}); err != nil {
		t.Fatal(err)
	}
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
	if got, want := strings.Join(github.branches, ","), "alpha,beta"; github.base != "main" || got != want {
		t.Errorf("Link = (--base %q %q), want (--base main alpha,beta)", github.base, got)
	}
}

func TestLinkApplyRevalidatesOnePullRequestWithoutGitHubMutation(t *testing.T) {
	github := &cliGitHub{}
	output, err := executeWithService(t, cliSingleBranchService(github), "link", "--apply")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if github.inspectCalls != 2 || github.links != 0 {
		t.Errorf("inspections=%d links=%d, want 2 and 0", github.inspectCalls, github.links)
	}
	if !strings.Contains(output, "Nothing to link — this stack has one pull request.") || !strings.Contains(output, "No changes were needed or made.") || strings.Contains(output, "gh stack link") || strings.Contains(output, "Ready to apply") || strings.Contains(output, "Applied —") {
		t.Errorf("output = %q", output)
	}
}

func TestLinkApplyRendersAndFlushesValidatedPlanBeforeMutation(t *testing.T) {
	events := []string{}
	writer := &recordingWriter{events: &events}
	github := &cliGitHub{events: &events}
	command := newWithPresentation("v0.2.4", "gt2gh", writer, writer, cliService(github), syncer.Service{}, push.Service{}, Presentation{})
	command.SetArgs([]string{"link", "--apply"})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	flush, link := eventIndex(events, "flush"), eventIndex(events, "link")
	inspections := eventIndexes(events, "inspect")
	firstWrite := eventIndex(events, "write")
	if len(inspections) != 2 || firstWrite < 0 || flush < 0 || link < 0 || inspections[1] > firstWrite || firstWrite > flush || flush > link {
		t.Errorf("events = %v, want flush before link", events)
	}
	output := writer.String()
	if strings.Count(output, "main (trunk)") != 1 || strings.Count(output, "gh stack link --base main alpha beta") != 1 || !strings.Contains(output, "Ready to apply") || !strings.Contains(output, "Applied — GitHub stack updated") {
		t.Errorf("output = %q", output)
	}
}

func TestLinkApplyDoesNotRenderReadyPlanWhenRevalidationIsCanceled(t *testing.T) {
	events := []string{}
	writer := &recordingWriter{events: &events}
	github := &cliGitHub{events: &events, inspectErrAt: 2, inspectErr: context.Canceled}
	command := newWithPresentation("v0.2.4", "gt2gh", writer, writer, cliService(github), syncer.Service{}, push.Service{}, Presentation{})
	command.SetArgs([]string{"link", "--apply"})
	if err := command.Execute(); err == nil {
		t.Fatal("Execute() error = nil")
	}
	output := writer.String()
	if strings.Contains(output, "Ready to apply") || strings.Contains(output, "Applied —") || eventIndex(events, "flush") >= 0 || eventIndex(events, "link") >= 0 || !strings.Contains(output, "Not applied\ncontext canceled") {
		t.Errorf("events = %v output = %q", events, output)
	}
}

func TestLinkApplyReportsCancellationWithoutSuccess(t *testing.T) {
	events := []string{}
	writer := &recordingWriter{events: &events}
	github := &cliGitHub{events: &events, linkErr: context.Canceled}
	command := newWithPresentation("v0.2.4", "gt2gh", writer, writer, cliService(github), syncer.Service{}, push.Service{}, Presentation{})
	command.SetArgs([]string{"link", "--apply"})
	err := command.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil")
	}
	output := writer.String()
	if !strings.Contains(output, "Ready to apply") || !strings.Contains(output, "Not applied\ncontext canceled") || strings.Contains(output, "Applied —") || strings.Contains(output, "Changes were made.") {
		t.Errorf("output = %q", output)
	}
	if flush, link := eventIndex(events, "flush"), eventIndex(events, "link"); flush < 0 || link < 0 || flush > link {
		t.Errorf("events = %v, want flush before link", events)
	}
}

func TestLinkApplyFailureOutputUsesOneBoundedDiagnosticBlock(t *testing.T) {
	for _, test := range []struct {
		name       string
		diagnostic string
	}{
		{name: "non-fast-forward", diagnostic: "! [rejected] synthetic-a -> synthetic-a (non-fast-forward)\nerror: failed to push synthetic refs"},
		{name: "generic", diagnostic: "synthetic gh stack failure"},
	} {
		t.Run(test.name, func(t *testing.T) {
			github := &cliGitHub{linkErr: &githubstack.CommandError{Command: "gh stack link --base main alpha beta", Cause: errors.New("exit status 1"), Output: test.diagnostic}}
			output, err := executeWithService(t, cliService(github), "link", "--apply")
			if err == nil {
				t.Fatal("Execute() error = nil")
			}
			if !strings.Contains(output, "\n\nNot applied\ngh stack link failed.\n\nDiagnostic:\n") || strings.Contains(output, "Applied —") || strings.Contains(output, "Changes were made.") {
				t.Errorf("output = %q", output)
			}
			for _, line := range strings.Split(test.diagnostic, "\n") {
				if strings.Count(output, line) != 1 {
					t.Errorf("diagnostic line %q appears %d times in %q", line, strings.Count(output, line), output)
				}
			}
		})
	}
}

func TestLinkApplyDoesNotMutateWhenReadyOutputCannotFlush(t *testing.T) {
	events := []string{}
	writer := &recordingWriter{events: &events, flushErr: context.Canceled}
	github := &cliGitHub{events: &events}
	command := newWithPresentation("v0.2.4", "gt2gh", writer, writer, cliService(github), syncer.Service{}, push.Service{}, Presentation{})
	command.SetArgs([]string{"link", "--apply"})
	if err := command.Execute(); err == nil {
		t.Fatal("Execute() error = nil")
	}
	if eventIndex(events, "link") >= 0 || !strings.Contains(writer.String(), "Not applied\nflush ready-to-apply output") {
		t.Errorf("events = %v output = %q", events, writer.String())
	}
}

func TestLinkApplyDoesNotMutateWhenReadyOutputCannotWrite(t *testing.T) {
	events := []string{}
	writer := &recordingWriter{events: &events, writeErr: context.Canceled}
	github := &cliGitHub{events: &events}
	command := newWithPresentation("v0.2.4", "gt2gh", writer, writer, cliService(github), syncer.Service{}, push.Service{}, Presentation{})
	command.SetArgs([]string{"link", "--apply"})
	if err := command.Execute(); err == nil {
		t.Fatal("Execute() error = nil")
	}
	if eventIndex(events, "link") >= 0 || eventIndex(events, "flush") >= 0 {
		t.Errorf("events = %v", events)
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

func TestPushBranchCompletionUsesTrackedLocalBranchNames(t *testing.T) {
	var stdout, stderr bytes.Buffer
	command := newWithPresentation("v", "gt2gh", &stdout, &stderr, cliService(&cliGitHub{}), syncer.Service{}, push.Service{Git: &cliPushGit{}, Graphite: cliPushGraphite{}}, Presentation{})
	command.SetArgs([]string{"__complete", "push", "--branch", "be"})
	err := command.Execute()
	output := stdout.String()
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
	for _, expected := range []string{"Target: beta", "main (trunk)", "alpha (#1) (aligned)", "beta (#2) (divergent: base main, want alpha)", "Command to run\ngh stack link --base main alpha beta", "No changes were made."} {
		if !strings.Contains(output, expected) {
			t.Errorf("preview missing %q:\n%s", expected, output)
		}
	}
	if github.links != 0 {
		t.Errorf("Link calls = %d, want 0", github.links)
	}
}

func TestSyncPlanSnapshotsUseOneSpacedGraphAndCopyableCommand(t *testing.T) {
	plan := syncer.Plan{
		Link: link.Plan{Target: "synthetic-top", Base: "synthetic-main", Branches: []string{"synthetic-a", "synthetic-b"}},
		Items: []syncer.Item{
			{Branch: "synthetic-a", ExpectedBase: "synthetic-main", State: syncer.Aligned, PullRequest: &githubstack.PullRequest{Number: 10}},
			{Branch: "synthetic-b", ExpectedBase: "synthetic-a", State: syncer.Divergent, PullRequest: &githubstack.PullRequest{Number: 11, Base: "synthetic-main"}},
		},
	}
	for _, test := range []struct {
		name         string
		presentation Presentation
		want         string
	}{
		{
			name: "plain",
			want: "Target: synthetic-top\n\n  synthetic-main (trunk)\n  └─ synthetic-a (#10) (aligned)\n    └─ synthetic-b (#11) (divergent: base synthetic-main, want synthetic-a)\n\nCommand to run\ngh stack link --base synthetic-main synthetic-a synthetic-b\n",
		},
		{
			name:         "color",
			presentation: Presentation{Color: true},
			want:         "\x1b[1;36mTarget\x1b[0m: \x1b[1;37msynthetic-top\x1b[0m\n\n  \x1b[1;33msynthetic-main (trunk)\x1b[0m\n  └─ \x1b[1;37msynthetic-a\x1b[0m (\x1b[35m#10\x1b[0m) \x1b[32m(aligned)\x1b[0m\n    └─ \x1b[1;37msynthetic-b\x1b[0m (\x1b[35m#11\x1b[0m) \x1b[1;38;5;214m(divergent: base synthetic-main, want synthetic-a)\x1b[0m\n\n\x1b[1;36mCommand to run\x1b[0m\n\x1b[1;97;48;5;236mgh stack link --base synthetic-main synthetic-a synthetic-b\x1b[0m\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			if err := writeSyncPlan(&output, plan, test.presentation); err != nil {
				t.Fatal(err)
			}
			if got := output.String(); got != test.want {
				t.Errorf("snapshot = %q, want %q", got, test.want)
			}
			for _, redundant := range []string{"Resolved target", "Graphite path", "bottom to top", "Proposed command", "Reconciliation summary"} {
				if strings.Contains(output.String(), redundant) {
					t.Errorf("snapshot contains %q: %q", redundant, output.String())
				}
			}
		})
	}
}

func TestSyncPreviewShowsMissingAndUnsafeNodesWithoutApplyCommandMutation(t *testing.T) {
	plan := syncer.Plan{
		Link: link.Plan{Target: "synthetic-top", Base: "synthetic-main", Branches: []string{"synthetic-a", "synthetic-b"}},
		Items: []syncer.Item{
			{Branch: "synthetic-a", ExpectedBase: "synthetic-main", State: syncer.Missing},
			{Branch: "synthetic-b", ExpectedBase: "synthetic-a", State: syncer.Unsafe, PullRequest: &githubstack.PullRequest{Number: 12, State: "CLOSED"}},
		},
	}
	var output bytes.Buffer
	if err := writeSyncPlan(&output, plan, Presentation{}); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"synthetic-a (missing pull request)", "synthetic-b (#12) (non-open pull request)", "Apply blocked"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("preview missing %q: %q", expected, output.String())
		}
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
	if !strings.Contains(output, "Ready to apply") || !strings.Contains(output, "Applied — GitHub stack updated") || !strings.Contains(output, "Changes were made.") {
		t.Errorf("output = %q", output)
	}
}

func TestSyncApplyFailureNeverReportsSuccess(t *testing.T) {
	discoverer := &cliApplyDiscoverer{}
	github := &cliSyncGitHub{err: errors.New("synthetic sync failure")}
	service := syncer.Service{Discoverer: discoverer, Git: cliSyncGit{}, GitHub: github}
	output, err := executeWithServices(t, cliService(&cliGitHub{}), service, "sync", "--apply")
	if err == nil {
		t.Fatal("Execute() error = nil")
	}
	if !strings.Contains(output, "Ready to apply") || !strings.Contains(output, "Not applied\nsynthetic sync failure") || strings.Contains(output, "Applied —") || strings.Contains(output, "Changes were made.") {
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

func cliSingleBranchService(github link.GitHub) link.Service {
	return link.Service{
		Git:      cliGit{current: "alpha", branches: []string{"main", "alpha"}},
		Graphite: cliSingleBranchGraphite{},
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
func (f cliGraphite) DiscoverStack(ctx context.Context, branch string, _ bool) (graphite.Stack, error) {
	return f.Discover(ctx, branch)
}
func (cliGraphite) TrackedBranches(context.Context) ([]string, error) {
	return []string{"alpha", "beta"}, nil
}

type cliSingleBranchGraphite struct{}

func (cliSingleBranchGraphite) Discover(_ context.Context, branch string) (graphite.Stack, error) {
	if branch != "alpha" {
		return graphite.Stack{}, context.Canceled
	}
	return graphite.Stack{Path: []string{"main", "alpha"}, Trunks: []string{"main"}}, nil
}
func (f cliSingleBranchGraphite) DiscoverStack(ctx context.Context, branch string, _ bool) (graphite.Stack, error) {
	return f.Discover(ctx, branch)
}
func (cliSingleBranchGraphite) TrackedBranches(context.Context) ([]string, error) {
	return []string{"alpha"}, nil
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

type recordingWriter struct {
	bytes.Buffer
	events   *[]string
	flushErr error
	writeErr error
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
func (f *cliGitHubMissing) Link(context.Context, string, []string) error { f.links++; return nil }

type cliGitHubPRs struct {
	prs   []githubstack.PullRequest
	links int
}

func (f *cliGitHubPRs) Inspect(context.Context, []string) ([]githubstack.PullRequest, error) {
	return f.prs, nil
}
func (f *cliGitHubPRs) Link(context.Context, string, []string) error { f.links++; return nil }

type cliSyncDiscoverer struct{}

func (cliSyncDiscoverer) DiscoverWithOptions(context.Context, link.Selection) (link.Plan, error) {
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

func (f *cliApplyDiscoverer) DiscoverWithOptions(_ context.Context, selection link.Selection) (link.Plan, error) {
	f.branch = selection.Branch
	return link.Plan{
		Target: "beta", TargetSource: "--branch", Base: "main", BaseSource: "Graphite-declared ancestry", GraphitePath: []string{"main", "alpha", "beta"}, Branches: []string{"alpha", "beta"},
		PullRequests: []githubstack.PullRequest{{Number: 1, Head: "alpha", Base: "main", State: "OPEN"}, {Number: 2, Head: "beta", Base: "alpha", State: "OPEN"}},
	}, nil
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

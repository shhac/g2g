package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/githubstack"
	"github.com/shhac/gt2gh/internal/link"
	"github.com/shhac/gt2gh/internal/push"
	"github.com/shhac/gt2gh/internal/stack"
	syncer "github.com/shhac/gt2gh/internal/sync"
)

func TestLinkPreviewPrintsResolvedTargetWithoutMutation(t *testing.T) {
	github := &cliGitHub{}
	output, err := executeWithService(t, cliService(github), "link", "--branch", "beta")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, expected := range []string{
		"Target  beta",
		"\u25cb main", "alpha", "#1", "beta", "#2",
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
	plan := link.Plan{Discovery: stack.Discovery{Snapshot: stack.Snapshot{Target: "beta", TargetSource: "--branch", Base: "main", Branches: []string{"alpha", "beta"}}, PullRequests: []githubstack.PullRequest{{Number: 1, Head: "alpha"}, {Number: 2, Head: "beta"}}}}
	for _, test := range []struct {
		color bool
		want  string
	}{{false, "  ○ main   trunk"}, {true, "\x1b[1;33mmain\x1b[0m"}} {
		presentation := Presentation{Color: test.color}
		var output bytes.Buffer
		if err := writeLinkPlan(&output, plan, presentation); err != nil {
			t.Fatal(err)
		}
		got := output.String()
		if !strings.Contains(got, test.want) || strings.Count(got, trunkGlyph) != 1 || strings.Count(got, "gh stack link --base main alpha beta") != 1 {
			t.Errorf("preview = %q", got)
		}
		if !test.color && strings.Contains(got, "\x1b[") {
			t.Errorf("plain preview contains escape sequences: %q", got)
		}
	}
}

func TestLinkPlanSnapshots(t *testing.T) {
	resolved := link.Plan{Discovery: stack.Discovery{Snapshot: stack.Snapshot{Target: "beta", TargetSource: "--branch", Base: "main", Branches: []string{"alpha", "beta"}}, PullRequests: []githubstack.PullRequest{{Number: 1, Head: "alpha"}, {Number: 2, Head: "beta"}}}}
	unresolved := resolved
	unresolved.Issues = []link.Issue{{Branch: "beta", Reason: "no open pull request"}}
	for _, test := range []struct {
		name         string
		plan         link.Plan
		presentation Presentation
	}{
		{name: "link-resolved-plain", plan: resolved},
		{name: "link-resolved-color", plan: resolved, presentation: Presentation{Color: true}},
		{name: "link-unresolved-plain", plan: unresolved},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			if err := writeLinkPlan(&output, test.plan, test.presentation); err != nil {
				t.Fatal(err)
			}
			assertGolden(t, test.name, output.String())
			if strings.Contains(output.String(), "Link stack") || strings.Contains(output.String(), "bottom to top") {
				t.Errorf("snapshot has redundant leader: %q", output.String())
			}
		})
	}
}

func TestLinkPreviewCommandLineIsBareAndHighlightedOnlyWithColor(t *testing.T) {
	plan := link.Plan{Discovery: stack.Discovery{Snapshot: stack.Snapshot{Target: "beta", TargetSource: "--branch", Base: "main", Branches: []string{"alpha", "beta"}}, PullRequests: []githubstack.PullRequest{{Number: 1, Head: "alpha"}, {Number: 2, Head: "beta"}}}}
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
			want := presentation.accent("Command to run") + "\n" + commandLine("gh stack link --base main alpha beta", presentation) + "\n"
			if got := output.String(); !strings.Contains(got, want) || strings.Contains(got, "$ gh stack link") || strings.Contains(got, "│gh stack") {
				t.Errorf("preview = %q", got)
			}
		})
	}
}

func TestLinkPreviewCommandUsesShellSafeArguments(t *testing.T) {
	plan := link.Plan{Discovery: stack.Discovery{Snapshot: stack.Snapshot{Target: "feature;synthetic", TargetSource: "--branch", Base: "main", Branches: []string{"alpha", "feature;synthetic"}}, PullRequests: []githubstack.PullRequest{{Number: 1, Head: "alpha"}, {Number: 2, Head: "feature;synthetic"}}}}
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
		{name: "current branch", args: []string{"link"}, want: "Target  alpha"},
		{name: "explicit branch", args: []string{"link", "--branch", "alpha"}, want: "Target  alpha"},
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
	if !strings.Contains(output, "Target  beta") {
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
	if !strings.Contains(output, "unresolved: no open pull request") || !strings.Contains(output, "Apply blocked") || github.links != 0 {
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
		{name: "missing", want: "unresolved: no open pull request"},
		{name: "non-open", prs: []githubstack.PullRequest{{Number: 1, Head: "alpha", Base: "main", State: "CLOSED"}}, want: "unresolved: closed pull request"},
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
	plan := link.Plan{Discovery: stack.Discovery{Snapshot: stack.Snapshot{Target: "beta-two", TargetSource: "--branch", Base: "main", Branches: []string{"alpha", "beta", "beta-two"}}, PullRequests: []githubstack.PullRequest{{Number: 1, Head: "alpha"}}}, Issues: []link.Issue{
		{Branch: "beta", Reason: "closed pull request"},
		{Branch: "beta-two", Reason: "no open pull request"},
	}}
	var output bytes.Buffer
	if err := writeLinkPlan(&output, plan, Presentation{}); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); !strings.Contains(got, "unresolved: closed pull request") || !strings.Contains(got, "unresolved: no open pull request") || strings.Contains(got, "(#0)") {
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
	if strings.Count(output, "\u25cb main") != 1 || strings.Count(output, "gh stack link --base main alpha beta") != 1 || !strings.Contains(output, "Ready to apply") || !strings.Contains(output, "Applied — GitHub stack updated") {
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

func (f *cliGitHubPRs) Link(context.Context, string, []string) error { f.links++; return nil }

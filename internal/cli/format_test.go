package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/githubstack"
	"github.com/shhac/gt2gh/internal/link"
	"github.com/shhac/gt2gh/internal/stack"
)

func formatPlan() link.Plan {
	return link.Plan{Discovery: stack.Discovery{Snapshot: stack.Snapshot{Target: "beta", TargetSource: "--branch", Base: "main", Branches: []string{"alpha", "beta"}}, PullRequests: []githubstack.PullRequest{
		{Number: 1, Head: "alpha", URL: "https://example.test/1", State: "OPEN"},
		{Number: 2, Head: "beta", URL: "https://example.test/2", State: "OPEN"},
	}}}
}

// The machine formats exist so callers stop parsing decorated terminal text.
// Decoding must therefore recover the same facts the graph shows.
func TestJSONDocumentCarriesTheRenderedPlan(t *testing.T) {
	var output bytes.Buffer
	if err := writeLinkPlan(&output, formatPlan(), Presentation{Format: formatJSON}); err != nil {
		t.Fatal(err)
	}

	var doc jsonDocument
	if err := json.Unmarshal(output.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v\n%s", err, output.String())
	}

	if doc.SchemaVersion != schemaVersion {
		t.Errorf("schemaVersion = %d, want %d", doc.SchemaVersion, schemaVersion)
	}
	if doc.Operation != "link" || doc.Target != "beta" || doc.TargetSource != "--branch" || doc.Trunk != "main" {
		t.Errorf("header = %#v", doc)
	}
	if len(doc.Branches) != 2 {
		t.Fatalf("branches = %#v, want 2", doc.Branches)
	}
	if doc.Branches[0].Branch != "alpha" || doc.Branches[0].PullRequest != 1 || doc.Branches[0].URL != "https://example.test/1" {
		t.Errorf("first branch = %#v", doc.Branches[0])
	}
	if !doc.Branches[1].Target {
		t.Errorf("target branch not marked: %#v", doc.Branches[1])
	}
	if got, want := strings.Join(doc.Command, " "), "gh stack link --base main alpha beta"; got != want {
		t.Errorf("command = %q, want %q", got, want)
	}
}

// The trunk is a node in the graph but a distinct field in the document, so a
// caller never has to detect it positionally.
func TestJSONSeparatesTrunkFromStackedBranches(t *testing.T) {
	var output bytes.Buffer
	if err := writeLinkPlan(&output, formatPlan(), Presentation{Format: formatJSON}); err != nil {
		t.Fatal(err)
	}

	var doc jsonDocument
	if err := json.Unmarshal(output.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	for _, branch := range doc.Branches {
		if branch.Branch == doc.Trunk {
			t.Errorf("trunk %q also listed as a stacked branch", doc.Trunk)
		}
	}
}

// A blocked plan still reports its command. The command is the plan's known
// destination, and a consumer decides for itself after checking blocked.
func TestJSONReportsBlockedStateAlongsideTheCommand(t *testing.T) {
	plan := formatPlan()
	plan.Issues = []link.Issue{{Branch: "beta", Reason: "no open pull request"}}

	var output bytes.Buffer
	if err := writeLinkPlan(&output, plan, Presentation{Format: formatJSON}); err != nil {
		t.Fatal(err)
	}

	var doc jsonDocument
	if err := json.Unmarshal(output.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Blocked == "" {
		t.Errorf("blocked document did not report a reason: %#v", doc)
	}
	if got, want := strings.Join(doc.Command, " "), "gh stack link --base main alpha beta"; got != want {
		t.Errorf("command = %q, want %q", got, want)
	}
	if doc.Branches[1].Severity != string(severityBad) || !strings.Contains(doc.Branches[1].State, "no open pull request") {
		t.Errorf("blocked branch = %#v", doc.Branches[1])
	}
}

// The only command that is withheld is one that cannot be constructed:
// gh stack link needs two branches. This used to be decided via NothingToLink,
// which also folds in the issue check, so a blocked single-branch path
// rendered a one-branch command that could never be valid.
func TestSingleBranchPathNeverRendersAnUnusableCommand(t *testing.T) {
	for _, test := range []struct {
		name   string
		issues []link.Issue
	}{
		{name: "clean"},
		{name: "blocked", issues: []link.Issue{{Branch: "alpha", Reason: "no open pull request"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := link.Plan{Discovery: stack.Discovery{Snapshot: stack.Snapshot{Target: "alpha", Base: "main", Branches: []string{"alpha"}}, PullRequests: []githubstack.PullRequest{{Number: 1, Head: "alpha", State: "OPEN"}}}, Issues: test.issues}
			var output bytes.Buffer
			if err := writeLinkPlan(&output, plan, Presentation{}); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(output.String(), "gh stack link") {
				t.Errorf("single-branch path rendered a command:\n%s", output.String())
			}
		})
	}
}

func TestPorcelainEmitsStableTabSeparatedRecords(t *testing.T) {
	var output bytes.Buffer
	if err := writeLinkPlan(&output, formatPlan(), Presentation{Format: formatPorcelain}); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimRight(output.String(), "\n"), "\n")
	if got, want := len(lines), 5; got != want {
		t.Fatalf("records = %d, want %d:\n%s", got, want, output.String())
	}
	for index, want := range []string{"target", "trunk", "branch", "branch", "command"} {
		if got := strings.Split(lines[index], "\t")[0]; got != want {
			t.Errorf("record %d type = %q, want %q", index, got, want)
		}
	}
	if got, want := lines[0], "target\tbeta\t--branch"; got != want {
		t.Errorf("target record = %q, want %q", got, want)
	}
	if got, want := lines[4], "command\tgh\tstack\tlink\t--base\tmain\talpha\tbeta"; got != want {
		t.Errorf("command record = %q, want %q", got, want)
	}
}

// A machine format must be parseable on its own, so neither ANSI decoration
// nor the human closing notices may reach it.
func TestMachineFormatsEmitOnlyTheDocument(t *testing.T) {
	for _, format := range []outputFormat{formatJSON, formatPorcelain} {
		t.Run(string(format), func(t *testing.T) {
			github := &cliGitHub{}
			var stdout, stderr bytes.Buffer
			command := NewWithService("v", &stdout, &stderr, cliService(github))
			command.SetArgs([]string{"link", "--branch", "beta", "--" + string(format)})
			if err := command.Execute(); err != nil {
				t.Fatal(err)
			}

			got := stdout.String()
			if strings.Contains(got, "\x1b[") {
				t.Errorf("machine output contains ANSI: %q", got)
			}
			for _, unwanted := range []string{"No changes were made", "Command to run", "Target  ", trunkGlyph, branchGlyph} {
				if strings.Contains(got, unwanted) {
					t.Errorf("machine output contains human text %q:\n%s", unwanted, got)
				}
			}
		})
	}
}

func TestJSONIsRequestedPerInvocationNotAtConstruction(t *testing.T) {
	github := &cliGitHub{}
	var stdout, stderr bytes.Buffer
	command := NewWithService("v", &stdout, &stderr, cliService(github))
	command.SetArgs([]string{"link", "--branch", "beta"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Command to run") {
		t.Errorf("default output was not the human preview:\n%s", stdout.String())
	}
}

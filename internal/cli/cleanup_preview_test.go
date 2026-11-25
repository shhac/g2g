package cli

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/prune"
	"github.com/shhac/g2g/internal/push"
	"github.com/shhac/g2g/internal/shape"
	"github.com/spf13/cobra"
)

func TestMissingHintsGroupOnlyEntirelyMissingSelectedSubtrees(t *testing.T) {
	for _, test := range []struct {
		name     string
		branches []string
		states   map[string]graph.NodeState
		grouped  bool
	}{
		{"whole chain", []string{"synthetic-auth", "synthetic-login", "synthetic-session"}, map[string]graph.NodeState{"synthetic-auth": graph.StateBranchMissing, "synthetic-login": graph.StateBranchMissing, "synthetic-session": graph.StateBranchMissing}, true},
		{"live child", []string{"synthetic-auth", "synthetic-login", "synthetic-session"}, map[string]graph.NodeState{"synthetic-auth": graph.StateBranchMissing, "synthetic-login": graph.StateAligned, "synthetic-session": graph.StateBranchMissing}, false},
		{"child outside selection", []string{"synthetic-auth"}, map[string]graph.NodeState{"synthetic-auth": graph.StateBranchMissing}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			notes := missingNotes(graph.Discovery{Graph: graphFixture(), Branches: test.branches, States: test.states})
			text := plainCommands(strings.Join(notes, "\n"))
			if strings.Contains(text, "--scope subtree") != test.grouped {
				t.Fatalf("hints = %s", text)
			}
			if test.grouped && (len(notes) != 1 || !strings.Contains(text, "g2g untrack --branch synthetic-auth --scope subtree")) {
				t.Fatalf("did not consolidate: %s", text)
			}
		})
	}
}

func TestLandedHintPreservesTheSelection(t *testing.T) {
	discovery := graph.Discovery{Graph: graphFixture(), Target: "synthetic-auth", TargetSource: shape.TargetNamed, Scope: graph.ScopeAll, Branches: []string{"synthetic-auth"}, States: map[string]graph.NodeState{"synthetic-auth": graph.StateLanded}}
	text := ""
	for _, note := range statusView(discovery).Notes {
		text += plainCommands(note.Text)
	}
	if !strings.Contains(text, "g2g prune --branch synthetic-auth --scope all") || !strings.Contains(text, "--delete-branches") {
		t.Fatal(text)
	}
}

type observationFixture map[string]githubstack.Observation

func (f observationFixture) Load(context.Context) (map[string]githubstack.Observation, error) {
	return f, nil
}

func TestOfflinePRKnowledgeIsDatedAndDoesNotDuplicateMarks(t *testing.T) {
	d := graph.Discovery{Graph: graphFixture(), Target: "synthetic-auth", Scope: graph.ScopeStack, Branches: []string{"synthetic-auth", "synthetic-login"}, States: map[string]graph.NodeState{"synthetic-login": graph.StateBranchMissing}}
	seen := observationFixture{"synthetic-auth": {PullRequest: githubstack.PullRequest{Number: 41, URL: "https://example.test/synthetic/repo/pull/41", State: "MERGED"}, ObservedAt: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}}
	view := markPublished(statusView(d), selectedIn(d).from("origin"), map[string]push.Publication{"synthetic-auth": {Standing: push.Current}})
	view = rememberedPRs(context.Background(), view, d, seen)
	if strings.Count(view.Nodes[0].state(), "origin✓") != 1 || len(view.Nodes[0].Marks) != 2 {
		t.Fatalf("repeated marks: %+v", view.Nodes[0])
	}
	if !strings.Contains(view.Nodes[0].state(), "last seen merged 2026-01-01T12:00:00Z") || strings.Contains(view.Nodes[0].state(), "pr✗") {
		t.Fatalf("misleading PR state: %+v", view.Nodes[0])
	}
	if !strings.Contains(view.Nodes[1].state(), "PR history unknown") || strings.Contains(view.Nodes[1].state(), "never submitted") {
		t.Fatalf("invented PR history: %+v", view.Nodes[1])
	}
}

func TestCleanupReportsCompletedDeletionAndScopedRetryAsPartWay(t *testing.T) {
	var output bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&output)
	plan := prune.Plan{Landed: []string{"synthetic-work"}}
	stopped := &prune.Stopped{
		Deleted: []string{"synthetic-work"}, Err: errors.New("synthetic graph write failure"),
		Retry: "g2g prune --branch synthetic-main --scope all --forget-missing --delete-branches",
	}
	flow := pruneFlow(prune.Service{}, push.Known{}, localgit.DefaultRemote, graph.Selection{}, nil, cmd, Presentation{}, prune.Options{DeleteBranches: true}, nil)
	flow.plan = func(context.Context) (prunePlan, error) { return prunePlan{Plan: plan}, nil }
	// Nothing here is published, so there is nothing for settle to ask.
	flow.settle = nil
	flow.execute = func(context.Context, prunePlan) error { return stopped }
	err := flow.run(cmd, context.Background(), newBudgets(cmd), Presentation{}, true)
	if exitCode(err) != stoppedExitCode {
		t.Fatalf("cleanup exit = %d, %v", exitCode(err), err)
	}
	for _, want := range []string{"synthetic graph write failure", "Deleted local branches synthetic-work", stopped.Retry} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %q in report:\n%s", want, output.String())
		}
	}
	if strings.Contains(output.String(), "Not applied") {
		t.Fatal("completed cleanup was reported as unapplied")
	}
	output.Reset()
	if err := stoppedAfterPull(cmd, err, Presentation{}); exitCode(err) != stoppedExitCode || strings.Contains(output.String(), "nothing was forgotten") {
		t.Fatalf("pull composition hid cleanup: %v\n%s", err, output.String())
	}
}

// A trunk is landed on, not opened as a pull request, so a remembered pull
// request with a trunk's name as its head is somebody else's — a release from
// it into another branch — and must not be drawn as the trunk's own. A trunk
// declared to land somewhere is the exception: that pull request is how it
// lands.
func TestRememberedPRsAreDrawnOnATrunkOnlyWhenItLandsSomewhere(t *testing.T) {
	seen := observationFixture{"synthetic-main": {PullRequest: githubstack.PullRequest{Number: 7, URL: "https://example.test/synthetic/repo/pull/7", State: "CLOSED"}, ObservedAt: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}}
	for _, test := range []struct {
		name  string
		lands bool
	}{
		{name: "a trunk", lands: false},
		{name: "a trunk that lands", lands: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorded := graphFixture()
			if test.lands {
				recorded.Declared = map[string]graph.Declaration{"synthetic-main": {Into: "synthetic-release", By: "squash"}}
			}
			d := graph.Discovery{Graph: recorded, Target: "synthetic-main", Scope: graph.ScopeStack, Branches: []string{"synthetic-main", "synthetic-auth"}}
			view := rememberedPRs(context.Background(), statusView(d), d, seen)
			if !view.Nodes[0].Trunk {
				t.Fatalf("first node = %+v, want the trunk", view.Nodes[0])
			}
			if drawn := strings.Contains(view.Nodes[0].state(), "PR last seen closed"); drawn != test.lands {
				t.Errorf("trunk state = %q, want the remembered pull request drawn: %t", view.Nodes[0].state(), test.lands)
			}
		})
	}
}

// prune deletes only branches it records, so a graph-only preview has to name
// --delete-branches before the branches are forgotten: afterwards g2g can no
// longer remove them, which is how a whole landed stack was left behind as
// local branches nothing would clean up.
func TestAGraphOnlyPruneNamesDeletionBeforeItForgets(t *testing.T) {
	d := graph.Discovery{Graph: graphFixture(), Target: "synthetic-auth", TargetSource: shape.TargetNamed, Scope: graph.ScopeStack, Branches: []string{"synthetic-main", "synthetic-auth"}}
	for _, test := range []struct {
		name    string
		plan    prune.Plan
		want    string
		without string
	}{
		{
			name: "landed branches kept",
			plan: prune.Plan{Discovery: d, Landed: []string{"synthetic-auth"}},
			want: "No branch is deleted · run g2g prune --branch synthetic-auth --delete-branches to remove it too, which g2g cannot do once it is forgotten.",
		},
		{
			name: "missing records forgotten too",
			plan: prune.Plan{Discovery: d, Landed: []string{"synthetic-auth"}, Options: prune.Options{ForgetMissing: true}},
			want: "--delete-branches --forget-missing to remove it too",
		},
		{
			name:    "only missing records",
			plan:    prune.Plan{Discovery: d, ForgottenMissing: []string{"synthetic-login"}, Options: prune.Options{ForgetMissing: true}},
			want:    "No branch is deleted.",
			without: "--delete-branches",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var notes []string
			for _, note := range pruneView(test.plan).Notes {
				notes = append(notes, plainCommands(note.Text))
			}
			text := strings.Join(notes, "\n")
			if !strings.Contains(text, test.want) {
				t.Errorf("notes do not say %q:\n%s", test.want, text)
			}
			if test.without != "" && strings.Contains(text, test.without) {
				t.Errorf("notes say %q:\n%s", test.without, text)
			}
		})
	}
}

// A merged pull request is not evidence that what a branch holds here landed,
// so prune keeps the branch. It says so, and names the command that deletes
// one on purpose, rather than keeping it without a word beside a status that
// says the pull request merged.
func TestPruneNamesABranchItKeepsThoughItsPullRequestMerged(t *testing.T) {
	merged := githubstack.Observation{PullRequest: githubstack.PullRequest{Number: 1, State: "MERGED"}}
	open := githubstack.Observation{PullRequest: githubstack.PullRequest{Number: 2, State: "OPEN"}}
	recorded := graphFixture()
	branches := make([]string, 0, len(recorded.Edges))
	observed := observationFixture{}
	for branch := range recorded.Edges {
		branches = append(branches, branch)
		observed[branch] = merged
	}
	slices.Sort(branches)
	landed, kept, unmerged := branches[0], branches[1], branches[2]
	observed[unmerged], observed[branches[3]] = open, open
	plan := prune.Plan{Discovery: graph.Discovery{Graph: recorded, Branches: branches, States: map[string]graph.NodeState{}}, Landed: []string{landed}}

	got := mergedButKept(context.Background(), observed, plan)
	if !slices.Equal(got, []string{kept}) {
		t.Fatalf("merged but kept = %v, want only %s: %s landed and %s is still open", got, kept, landed, unmerged)
	}
	text := ""
	for _, note := range mergedNotes(stackView{}, prunePlan{Plan: plan, merged: got}).Notes {
		text += plainCommands(note.Text) + "\n"
	}
	for _, want := range []string{"Kept, though its pull request was last seen merged: " + kept, "g2g delete --branch " + kept} {
		if !strings.Contains(text, want) {
			t.Errorf("notes omit %q:\n%s", want, text)
		}
	}
	if mergedButKept(context.Background(), nil, plan) != nil {
		t.Error("a build with no remembered pull requests named some")
	}
}

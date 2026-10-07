package cli_test

import (
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/cli"
	"github.com/shhac/g2g/internal/testutil"
)

// Both pull requests sit on the wrong base, so a retarget moves two.
var misbasedPullRequests = `{"data":{"repository":{` +
	`"pr0":{"nodes":[{"number":201,"url":"https://example.test/201","headRefName":"synthetic-lower","baseRefName":"synthetic-other","state":"OPEN"}]},` +
	`"pr1":{"nodes":[{"number":202,"url":"https://example.test/202","headRefName":"synthetic-top","baseRefName":"synthetic-trunk","state":"OPEN"}]},` +
	`"pr2":{"nodes":[]}}}}`

// A base that moved stays moved, so a run that fails on the second pull
// request did something and must not say "Not applied" or exit as a failure.
// The retry it names is aimed at the stack it acted on.
func TestRetargetThatStopsPartWaySaysWhatItMoved(t *testing.T) {
	second := testutil.Route{Prefix: "pr edit 202", Stderr: "synthetic refusal", Exit: 1}
	recorder, _ := g2gOwnedRepositoryWithConversations(t, ownedGraph, misbasedPullRequests, ownedConversations, second)

	stdout, _, err := run(t, "github", "retarget", "--branch", "synthetic-top", "--apply")
	if err == nil {
		t.Fatalf("retarget --apply succeeded with a failing edit:\n%s", stdout)
	}
	if strings.Contains(stdout, "Not applied") {
		t.Errorf("a run that moved a base says nothing was applied:\n%s", stdout)
	}
	for _, want := range []string{
		"Stopped part-way at #202",
		"Moved the base of #201, and it stays moved.",
		"Rerun g2g github retarget --branch synthetic-top --apply to finish",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("report missing %q:\n%s", want, stdout)
		}
	}
	if !cli.StoppedPartWayForTest(err) {
		t.Errorf("error = %v, want the part-way status", err)
	}
	if got := recorder.Find("gh pr edit 201"); !strings.Contains(got, "--base synthetic-trunk") {
		t.Errorf("first edit = %q, want #201 moved onto synthetic-trunk", got)
	}
}

// Failing on the first pull request moved nothing, which is the ordinary
// failure.
func TestRetargetThatFailsFirstIsNotApplied(t *testing.T) {
	first := testutil.Route{Prefix: "pr edit 201", Stderr: "synthetic refusal", Exit: 1}
	g2gOwnedRepositoryWithConversations(t, ownedGraph, misbasedPullRequests, ownedConversations, first)

	stdout, _, err := run(t, "github", "retarget", "--apply")
	if err == nil || cli.StoppedPartWayForTest(err) {
		t.Fatalf("error = %v, want an ordinary failure", err)
	}
	if !strings.Contains(stdout, "Not applied") {
		t.Errorf("a run that moved nothing does not say so:\n%s", stdout)
	}
}

package cli_test

import (
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/cli"
	"github.com/shhac/g2g/internal/testutil"
)

// Both pull requests sit on the wrong base, so a retarget moves two.
var misbasedPullRequests = `{"data":{"repository":{` +
	`"pr0":{"nodes":[{"id":"PR_synthetic_201","number":201,"url":"https://example.test/201","headRefName":"synthetic-lower","baseRefName":"synthetic-other","state":"OPEN"}]},` +
	`"pr1":{"nodes":[{"id":"PR_synthetic_202","number":202,"url":"https://example.test/202","headRefName":"synthetic-top","baseRefName":"synthetic-trunk","state":"OPEN"}]},` +
	`"pr2":{"nodes":[]}}}}`

// Both bases move in one request, each addressed by the node id it was read
// with, bottom-up.
func TestRetargetMovesEveryBaseInOneRequest(t *testing.T) {
	recorder, _ := g2gOwnedRepositoryWithPullRequests(t, ownedGraph, misbasedPullRequests)

	if _, _, err := run(t, "github", "retarget", "--apply"); err != nil {
		t.Fatalf("retarget --apply: %v", err)
	}
	if got := recorder.Count("gh " + retargetMutationPrefix); got != 1 {
		t.Errorf("retarget requests = %d, want one", got)
	}
	if got := mutationAliases(recorder, "updatePullRequest"); got != 2 {
		t.Errorf("bases moved = %d, want two", got)
	}
	sent := recorder.Find("gh " + retargetMutationPrefix)
	if !strings.Contains(sent, "-f pullRequest0=PR_synthetic_201 -f base0=synthetic-trunk -f pullRequest1=PR_synthetic_202 -f base1=synthetic-lower") {
		t.Errorf("request = %q, want #201 onto synthetic-trunk and #202 onto synthetic-lower", sent)
	}
	recorder.AssertNone("gh pr ")
}

// GitHub refused the first change and applied the second. That used to stop
// the run before the second was tried; now the base that moved stays moved,
// so the run did something and must not say "Not applied" or exit as a
// failure. The retry it names is aimed at the stack it acted on.
func TestRetargetThatStopsPartWaySaysWhatItMoved(t *testing.T) {
	refused := testutil.Route{
		Prefix: retargetMutationPrefix,
		Output: `{"data":{"w0":null,"w1":{"clientMutationId":null}},"errors":[{"type":"UNPROCESSABLE","path":["w0"],"message":"synthetic refusal"}]}`,
		Exit:   1,
	}
	recorder, _ := g2gOwnedRepositoryWithConversations(t, ownedGraph, misbasedPullRequests, ownedConversations, refused)

	stdout, _, err := run(t, "github", "retarget", "--branch", "synthetic-top", "--apply")
	if err == nil {
		t.Fatalf("retarget --apply succeeded with a refused change:\n%s", stdout)
	}
	if strings.Contains(stdout, "Not applied") {
		t.Errorf("a run that moved a base says nothing was applied:\n%s", stdout)
	}
	for _, want := range []string{
		"Stopped part-way, with the base not moved on #201: synthetic refusal",
		"Moved the base of #202, and it stays moved.",
		"Rerun g2g github retarget --branch synthetic-top --apply to finish",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("report missing %q:\n%s", want, stdout)
		}
	}
	if !cli.StoppedPartWayForTest(err) {
		t.Errorf("error = %v, want the part-way status", err)
	}
	if got := recorder.Count("gh " + retargetMutationPrefix); got != 1 {
		t.Errorf("retarget requests = %d, want both changes in one", got)
	}
}

// Moving nothing is the ordinary failure, whether GitHub refused every change
// or the request never reached it.
func TestRetargetThatMovesNothingIsNotApplied(t *testing.T) {
	for name, route := range map[string]testutil.Route{
		"every change refused": {
			Prefix: retargetMutationPrefix,
			Output: `{"data":{"w0":null,"w1":null},"errors":[{"path":["w0"],"message":"synthetic refusal"},{"path":["w1"],"message":"synthetic refusal"}]}`,
			Exit:   1,
		},
		"request failed": {Prefix: retargetMutationPrefix, Stderr: "synthetic refusal", Exit: 1},
	} {
		t.Run(name, func(t *testing.T) {
			g2gOwnedRepositoryWithConversations(t, ownedGraph, misbasedPullRequests, ownedConversations, route)

			stdout, stderr, err := run(t, "github", "retarget", "--apply")
			if err == nil || cli.StoppedPartWayForTest(err) {
				t.Fatalf("error = %v, want an ordinary failure", err)
			}
			if !strings.Contains(stdout, "Not applied") {
				t.Errorf("a run that moved nothing does not say so:\n%s", stdout)
			}
			if !strings.Contains(stdout+stderr, "synthetic refusal") {
				t.Errorf("what GitHub said is lost:\n%s\n%s", stdout, stderr)
			}
		})
	}
}

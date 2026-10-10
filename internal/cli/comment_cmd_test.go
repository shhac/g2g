package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/cli"
	"github.com/shhac/g2g/internal/testutil"
)

// The preview reads GitHub and writes nothing; it shows what each pull request
// would get and one of the comments in full.
func TestCommentPreviewsWithoutWriting(t *testing.T) {
	recorder, _ := g2gOwnedRepository(t, ownedGraph)

	stdout, _, err := run(t, "github", "comment")
	if err != nil {
		t.Fatalf("comment: %v\n%s", err, stdout)
	}
	for _, want := range []string{
		"synthetic-lower", "synthetic-top", "comment✗ none yet · would add",
		"The comment on #202",
		"- **#202 `synthetic-top`** 👈 this pull request",
		"Rerun with --apply",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("preview missing %q:\n%s", want, stdout)
		}
	}
	recorder.AssertNone("gh "+commentMutationPrefix, "gt ")
	// One read of the pull requests, one of their conversations.
	recorder.AssertOrder("gh api graphql -F owner={owner} -F name={repo} -f query=query($owner", "gh "+stackCommentsPrefix)
}

// A stack of one pull request gets no comment, and the preview says so rather
// than reading as though a comment were already there.
func TestCommentSaysAStackOfOnePullRequestGetsNone(t *testing.T) {
	onePullRequest := `{"data":{"repository":{` +
		`"pr0":{"nodes":[{"id":"PR_synthetic_201","number":201,"url":"https://example.test/201","headRefName":"synthetic-lower","baseRefName":"synthetic-trunk","state":"OPEN"}]},` +
		`"pr1":{"nodes":[]},"pr2":{"nodes":[]}}}}`
	recorder, _ := g2gOwnedRepositoryWithPullRequests(t, ownedGraph, onePullRequest)

	stdout, _, err := run(t, "github", "comment")
	if err != nil {
		t.Fatalf("comment: %v\n%s", err, stdout)
	}
	for _, want := range []string{
		"no comment · the only pull request in its stack",
		"A stack listing one pull request gets no stack comment",
		"No stack comment needs adding or editing.",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("preview missing %q:\n%s", want, stdout)
		}
	}
	recorder.AssertNone("gh " + commentMutationPrefix)
}

func TestCommentApplyAddsOneCommentToEachPullRequest(t *testing.T) {
	recorder, _ := g2gOwnedRepository(t, ownedGraph)

	stdout, _, err := run(t, "github", "comment", "--apply")
	if err != nil {
		t.Fatalf("comment --apply: %v\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "Commented.") {
		t.Errorf("apply does not confirm:\n%s", stdout)
	}
	if got := mutationAliases(recorder, "addComment"); got != 2 {
		t.Fatalf("comments added = %d, want one per pull request:\n%s", got, strings.Join(recorder.Calls(), "\n"))
	}
	if got := recorder.Count("gh " + commentMutationPrefix); got != 1 {
		t.Errorf("comment requests = %d, want both comments in one", got)
	}
	calls := strings.Join(recorder.Calls(), "\n")
	for _, want := range []string{"-f subject0=PR_synthetic_201 -f body0=<!-- g2g:stack-comment rev=", "-f subject1=PR_synthetic_202 -f body1=<!-- g2g:stack-comment rev="} {
		if !strings.Contains(calls, want) {
			t.Errorf("no write %q in:\n%s", want, calls)
		}
	}
	if got := mutationAliases(recorder, "updateIssueComment"); got != 0 {
		t.Errorf("comments edited = %d, want none where there was nothing to edit", got)
	}
	recorder.AssertNone("gh pr ", "git push", "gt ")
}

// A pull request that already carries the comment is edited, never given a
// second one.
func TestCommentApplyEditsTheCommentItFinds(t *testing.T) {
	conversations := strings.Replace(ownedConversations,
		`"id":"PR_synthetic_201","number":201,"url":"https://example.test/201","headRefName":"synthetic-lower","baseRefName":"synthetic-trunk","state":"OPEN","locked":false,"comments":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[]}`,
		`"id":"PR_synthetic_201","number":201,"url":"https://example.test/201","headRefName":"synthetic-lower","baseRefName":"synthetic-trunk","state":"OPEN","locked":false,"comments":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[`+
			`{"id":"IC_synthetic_reviewer","body":"a reviewer's words","viewerCanUpdate":false,"author":{"login":"synthetic-reviewer"}},`+
			`{"id":"IC_synthetic_stale","body":"<!-- g2g:stack-comment -->\nstale","viewerCanUpdate":true,"author":{"login":"synthetic-author"}}]}`, 1)
	recorder, _ := g2gOwnedRepositoryWithConversations(t, ownedGraph, ownedPullRequests, conversations)

	if _, _, err := run(t, "github", "comment", "--apply"); err != nil {
		t.Fatalf("comment --apply: %v", err)
	}
	edited := recorder.Find("gh " + commentMutationPrefix)
	if !strings.Contains(edited, "w0: updateIssueComment(") || !strings.Contains(edited, "-f comment0=IC_synthetic_stale -f body0=<!-- g2g:stack-comment rev=") {
		t.Errorf("edit = %q, want the stale comment replaced", edited)
	}
	if strings.Contains(strings.Join(recorder.Calls(), "\n"), "IC_synthetic_reviewer") {
		t.Error("a comment without the marker was touched")
	}
	if got := mutationAliases(recorder, "addComment"); got != 1 {
		t.Errorf("comments added = %d, want only #202 given a new comment", got)
	}
	if got := recorder.Count("gh " + commentMutationPrefix); got != 1 {
		t.Errorf("comment requests = %d, want the edit and the addition in one", got)
	}
}

// A machine gets every comment with its body; a person gets one.
func TestCommentJSONCarriesEveryBody(t *testing.T) {
	g2gOwnedRepository(t, ownedGraph)

	stdout, _, err := run(t, "github", "comment", "--json")
	if err != nil {
		t.Fatalf("comment --json: %v\n%s", err, stdout)
	}
	var document struct {
		Operation string `json:"operation"`
		Comments  []struct {
			PullRequest int    `json:"pullRequest"`
			Action      string `json:"action"`
			Body        string `json:"body"`
		} `json:"comments"`
	}
	if err := json.Unmarshal([]byte(stdout), &document); err != nil {
		t.Fatalf("not one JSON document: %v\n%s", err, stdout)
	}
	if document.Operation != "github comment" || len(document.Comments) != 2 {
		t.Fatalf("document = %+v", document)
	}
	for _, written := range document.Comments {
		if written.Action != "create" || !strings.HasPrefix(written.Body, "<!-- g2g:stack-comment rev=") {
			t.Errorf("comment = %+v", written)
		}
	}
}

// A document GitHub answered for in part: it refused the first write and
// applied the second, and gh exits non-zero for the refusal. The refused
// first write used to stop the run before the second was tried.
const firstCommentRefused = `{"data":{"w0":null,"w1":{"clientMutationId":null}},` +
	`"errors":[{"type":"FORBIDDEN","path":["w0"],"message":"synthetic refusal"}]}`

// A refused write no longer stops the run: GitHub applied the rest of the
// request, so the report says which comment was written, which was not and
// why, and that rerunning finishes.
func TestCommentApplyThatStopsPartWaySaysWhatItWrote(t *testing.T) {
	refused := testutil.Route{Prefix: commentMutationPrefix, Output: firstCommentRefused, Exit: 1}
	recorder, _ := g2gOwnedRepositoryWithConversations(t, ownedGraph, ownedPullRequests, ownedConversations, refused)

	stdout, _, err := run(t, "github", "comment", "--branch", "synthetic-top", "--apply")
	if err == nil {
		t.Fatalf("comment --apply succeeded with a refused write:\n%s", stdout)
	}
	if strings.Contains(stdout, "Not applied") {
		t.Errorf("a run that wrote a comment says nothing was applied:\n%s", stdout)
	}
	// The retry keeps the stack the run was aimed at; a bare one would keep
	// whichever stack the checkout is on.
	for _, want := range []string{
		"Stopped part-way, with the comment not written on #201: synthetic refusal",
		"Wrote the comment on #202, and it stays.",
		"Rerun g2g github comment --branch synthetic-top --apply",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("report missing %q:\n%s", want, stdout)
		}
	}
	if !cli.StoppedPartWayForTest(err) {
		t.Errorf("error = %v, want the part-way status", err)
	}
	if documents, writes := recorder.Count("gh "+commentMutationPrefix), mutationAliases(recorder, "addComment"); documents != 1 || writes != 2 {
		t.Errorf("%d requests carrying %d additions, want both in one", documents, writes)
	}
}

// A request that never reached GitHub wrote nothing, which is the ordinary
// failure, with what gh said on stderr.
func TestCommentApplyThatWritesNothingIsNotApplied(t *testing.T) {
	failed := testutil.Route{Prefix: commentMutationPrefix, Stderr: "synthetic: could not reach GitHub", Exit: 1}
	g2gOwnedRepositoryWithConversations(t, ownedGraph, ownedPullRequests, ownedConversations, failed)

	stdout, stderr, err := run(t, "github", "comment", "--apply")
	if err == nil || cli.StoppedPartWayForTest(err) {
		t.Fatalf("error = %v, want an ordinary failure", err)
	}
	if !strings.Contains(stdout, "Not applied") {
		t.Errorf("a run that wrote nothing does not say so:\n%s", stdout)
	}
	if !strings.Contains(stdout+stderr, "synthetic: could not reach GitHub") {
		t.Errorf("what gh said is lost:\n%s\n%s", stdout, stderr)
	}
}

// Apply re-reads before it writes: the conversations are read twice, and every
// write comes after the second read.
func TestCommentApplyRevalidatesBeforeWriting(t *testing.T) {
	recorder, _ := g2gOwnedRepository(t, ownedGraph)

	if _, _, err := run(t, "github", "comment", "--apply"); err != nil {
		t.Fatalf("comment --apply: %v", err)
	}
	recorder.AssertOrder("gh "+stackCommentsPrefix, "gh "+stackCommentsPrefix, "gh "+commentMutationPrefix)
}

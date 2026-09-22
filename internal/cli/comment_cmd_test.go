package cli_test

import (
	"encoding/json"
	"strings"
	"testing"
)

// The preview reads GitHub and writes nothing; it shows what each pull request
// would get and one of the comments in full.
func TestCommentPreviewsWithoutWriting(t *testing.T) {
	recorder, _ := g2gOwnedRepository(t, ownedGraph)

	stdout, _, err := run(t, "comment")
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

func TestCommentApplyAddsOneCommentToEachPullRequest(t *testing.T) {
	recorder, _ := g2gOwnedRepository(t, ownedGraph)

	stdout, _, err := run(t, "comment", "--apply")
	if err != nil {
		t.Fatalf("comment --apply: %v\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "Commented.") {
		t.Errorf("apply does not confirm:\n%s", stdout)
	}
	if got := recorder.Count("gh " + commentMutationPrefix + "$subject"); got != 2 {
		t.Fatalf("addComment calls = %d, want one per pull request:\n%s", got, strings.Join(recorder.Calls(), "\n"))
	}
	calls := strings.Join(recorder.Calls(), "\n")
	for _, want := range []string{"-f subject=PR_synthetic_201 -f body=<!-- g2g:stack-comment -->", "-f subject=PR_synthetic_202 -f body=<!-- g2g:stack-comment -->"} {
		if !strings.Contains(calls, want) {
			t.Errorf("no write %q in:\n%s", want, calls)
		}
	}
	recorder.AssertNone("gh "+commentMutationPrefix+"$id", "gh pr ", "git push", "gt ")
}

// A pull request that already carries the comment is edited, never given a
// second one.
func TestCommentApplyEditsTheCommentItFinds(t *testing.T) {
	conversations := strings.Replace(ownedConversations,
		`"id":"PR_synthetic_201","number":201,"url":"https://example.test/201","headRefName":"synthetic-lower","baseRefName":"synthetic-trunk","state":"OPEN","viewerCanComment":true,"comments":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[]}`,
		`"id":"PR_synthetic_201","number":201,"url":"https://example.test/201","headRefName":"synthetic-lower","baseRefName":"synthetic-trunk","state":"OPEN","viewerCanComment":true,"comments":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[`+
			`{"id":"IC_synthetic_reviewer","body":"a reviewer's words","viewerCanUpdate":false,"author":{"login":"synthetic-reviewer"}},`+
			`{"id":"IC_synthetic_stale","body":"<!-- g2g:stack-comment -->\nstale","viewerCanUpdate":true,"author":{"login":"synthetic-author"}}]}`, 1)
	recorder, _ := g2gOwnedRepositoryWithConversations(t, ownedGraph, ownedPullRequests, conversations)

	if _, _, err := run(t, "comment", "--apply"); err != nil {
		t.Fatalf("comment --apply: %v", err)
	}
	edited := recorder.Find("gh " + commentMutationPrefix + "$id")
	if !strings.Contains(edited, "-f id=IC_synthetic_stale -f body=<!-- g2g:stack-comment -->") {
		t.Errorf("edit = %q, want the stale comment replaced", edited)
	}
	if strings.Contains(strings.Join(recorder.Calls(), "\n"), "IC_synthetic_reviewer") {
		t.Error("a comment without the marker was touched")
	}
	if got := recorder.Count("gh " + commentMutationPrefix + "$subject"); got != 1 {
		t.Errorf("addComment calls = %d, want only #202 given a new comment", got)
	}
}

// A machine gets every comment with its body; a person gets one.
func TestCommentJSONCarriesEveryBody(t *testing.T) {
	g2gOwnedRepository(t, ownedGraph)

	stdout, _, err := run(t, "comment", "--json")
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
	if document.Operation != "comment" || len(document.Comments) != 2 {
		t.Fatalf("document = %+v", document)
	}
	for _, written := range document.Comments {
		if written.Action != "create" || !strings.HasPrefix(written.Body, "<!-- g2g:stack-comment -->") {
			t.Errorf("comment = %+v", written)
		}
	}
}

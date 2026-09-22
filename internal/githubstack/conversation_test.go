package githubstack

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
)

const syntheticMarker = "<!-- synthetic-marker -->"

// scriptedRunner answers each gh call with the next scripted response and
// records the query it was sent, so a test can see which pull requests one
// round asked about.
type scriptedRunner struct {
	responses []string
	// failing makes the matching response come back with a non-zero exit, the
	// way gh reports a response that carries GraphQL errors.
	failing map[int]bool
	queries []string
	argv    [][]string
}

func (r *scriptedRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.argv = append(r.argv, append([]string{name}, args...))
	for _, argument := range args {
		if strings.HasPrefix(argument, "query=") {
			r.queries = append(r.queries, strings.TrimPrefix(argument, "query="))
		}
	}
	if len(r.responses) == 0 {
		return nil, fmt.Errorf("unscripted call: %v", args)
	}
	response := r.responses[0]
	r.responses = r.responses[1:]
	if r.failing[len(r.argv)-1] {
		return []byte(response + "\ngh: synthetic GraphQL failure\n"), fmt.Errorf("exit status 1")
	}
	return []byte(response), nil
}

func conversationJSON(alias string, number int, state string, hasNext bool, cursor string, comments ...string) string {
	nodes := make([]string, 0, len(comments))
	for index, body := range comments {
		nodes = append(nodes, fmt.Sprintf(`{"id":"IC_synthetic_%d_%d","body":%q,"viewerCanUpdate":true,"author":{"login":"synthetic-author"}}`, number, index, body))
	}
	return fmt.Sprintf(`%q:{"__typename":"PullRequest","id":"PR_synthetic_%d","number":%d,"url":"https://example.test/pull/%d","headRefName":"synthetic-%d","baseRefName":"synthetic-trunk","state":%q,"viewerCanComment":true,"comments":{"pageInfo":{"hasNextPage":%t,"endCursor":%q},"nodes":[%s]}}`,
		alias, number, number, number, number, state, hasNext, cursor, strings.Join(nodes, ","))
}

func repositoryJSON(fields ...string) string {
	return `{"data":{"repository":{` + strings.Join(fields, ",") + `}}}`
}

// A number that is an issue answers nothing, and one nothing answers to comes
// back as null with a NOT_FOUND error and a failing exit — which is what GitHub
// sends, and not a reason to lose the rest of the response.
func TestConversationsKeepsOnlyMarkedCommentsAndSkipsWhatIsNotAPullRequest(t *testing.T) {
	runner := &scriptedRunner{responses: []string{`{"data":{"repository":{` + strings.Join([]string{
		conversationJSON("c0", 11, "OPEN", false, "", "a reviewer's words", "quoting `"+syntheticMarker+"` in passing", "\n"+syntheticMarker+"\nsynthetic body"),
		`"c1":{"__typename":"Issue"}`,
		`"c2":null`,
	}, ",") + `}},"errors":[{"type":"NOT_FOUND","path":["repository","c2"],"message":"Could not resolve to an issue or pull request with the number of 13."}]}`}, failing: map[int]bool{0: true}}
	conversations, err := Client{Runner: runner}.Conversations(context.Background(), []int{11, 12, 13, 11}, syntheticMarker)
	if err != nil {
		t.Fatal(err)
	}
	if len(conversations) != 1 {
		t.Fatalf("conversations = %#v, want only the pull request", conversations)
	}
	got := conversations[0]
	if got.ID != "PR_synthetic_11" || got.State != "OPEN" || got.Head != "synthetic-11" || got.Base != "synthetic-trunk" || !got.Commentable {
		t.Errorf("conversation = %#v", got)
	}
	if len(got.Comments) != 1 || !strings.Contains(got.Comments[0].Body, "synthetic body") || !got.Comments[0].Editable || got.Comments[0].Author != "synthetic-author" {
		t.Errorf("comments = %#v, want only the comment that opens with the marker", got.Comments)
	}
	// A duplicate number is one pull request, asked about once.
	if strings.Count(runner.queries[0], "issueOrPullRequest") != 3 {
		t.Errorf("query = %q, want three distinct numbers", runner.queries[0])
	}
}

// A conversation longer than a page is read to its end, and only the pull
// request with another page is asked about again.
func TestConversationsFollowsOnlyThePagesThatContinue(t *testing.T) {
	runner := &scriptedRunner{responses: []string{
		repositoryJSON(
			conversationJSON("c0", 21, "OPEN", true, "synthetic-cursor", "first page"),
			conversationJSON("c1", 22, "MERGED", false, "", syntheticMarker+" done"),
		),
		repositoryJSON(conversationJSON("c0", 21, "OPEN", false, "", syntheticMarker+" second page")),
	}}
	conversations, err := Client{Runner: runner}.Conversations(context.Background(), []int{21, 22}, syntheticMarker)
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.queries) != 2 {
		t.Fatalf("queries = %d, want two rounds", len(runner.queries))
	}
	if !strings.Contains(runner.queries[1], `issueOrPullRequest(number: 21)`) || strings.Contains(runner.queries[1], "number: 22") || !strings.Contains(runner.queries[1], `after: "synthetic-cursor"`) {
		t.Errorf("second round = %q, want only #21 resumed at its cursor", runner.queries[1])
	}
	if len(conversations) != 2 || conversations[0].Number != 21 || len(conversations[0].Comments) != 1 || !strings.Contains(conversations[0].Comments[0].Body, "second page") {
		t.Errorf("conversations = %#v", conversations)
	}
}

func TestConversationsRefusesWhatItCannotTrust(t *testing.T) {
	notFoundElsewhere := `{"data":{"repository":{"c0":null}},"errors":[{"type":"NOT_FOUND","path":["repository","c9"],"message":"synthetic"}]}`
	forbidden := `{"data":{"repository":{"c0":null}},"errors":[{"type":"FORBIDDEN","path":["repository","c0"],"message":"synthetic"}]}`
	for _, response := range []string{notFoundElsewhere, forbidden} {
		runner := &scriptedRunner{responses: []string{response}, failing: map[int]bool{0: true}}
		if _, err := (Client{Runner: runner}).Conversations(context.Background(), []int{31}, syntheticMarker); err == nil {
			t.Errorf("Conversations() accepted a failure it cannot explain: %s", response)
		}
	}
	for name, response := range map[string]string{
		"errors":         `{"errors":[{"message":"synthetic failure"}]}`,
		"no repository":  `{"data":{}}`,
		"missing alias":  repositoryJSON(),
		"wrong number":   repositoryJSON(strings.Replace(conversationJSON("c0", 31, "OPEN", false, ""), `"number":31`, `"number":32`, 1)),
		"cursorless":     repositoryJSON(conversationJSON("c0", 31, "OPEN", true, "")),
		"comment w/o id": repositoryJSON(strings.Replace(conversationJSON("c0", 31, "OPEN", false, "", syntheticMarker), `"id":"IC_synthetic_31_0"`, `"id":""`, 1)),
	} {
		t.Run(name, func(t *testing.T) {
			runner := &scriptedRunner{responses: []string{response}}
			if _, err := (Client{Runner: runner}).Conversations(context.Background(), []int{31}, syntheticMarker); err == nil {
				t.Fatal("Conversations() succeeded, want a refusal")
			}
		})
	}
}

// The body is posted exactly as written: a raw field, never a typed one that
// would read a file for a leading @ or fill in {owner}.
func TestCommentMutationsSendTheBodyAsARawField(t *testing.T) {
	body := "@synthetic-file {owner} body\nsecond line"
	runner := &scriptedRunner{responses: []string{`{}`, `{}`}}
	client := Client{Runner: runner}
	if err := client.AddComment(context.Background(), "PR_synthetic", body); err != nil {
		t.Fatal(err)
	}
	if err := client.UpdateComment(context.Background(), "IC_synthetic", body); err != nil {
		t.Fatal(err)
	}
	for index, want := range []struct{ mutation, variable string }{
		{"addComment(input: {subjectId: $subject, body: $body})", "subject=PR_synthetic"},
		{"updateIssueComment(input: {id: $id, body: $body})", "id=IC_synthetic"},
	} {
		argv := strings.Join(runner.argv[index], "\x00")
		for _, fragment := range []string{"gh\x00api\x00graphql", want.mutation, "-f\x00" + want.variable, "-f\x00body=" + body} {
			if !strings.Contains(argv, fragment) {
				t.Errorf("call %d = %q, missing %q", index, runner.argv[index], fragment)
			}
		}
		if strings.Contains(argv, "-F\x00body") {
			t.Errorf("call %d sent the body as a typed field: %q", index, runner.argv[index])
		}
	}
}

// A failed write names the mutation and never repeats the body back.
func TestCommentMutationFailureDoesNotEchoTheBody(t *testing.T) {
	arguments := filepath.Join(t.TempDir(), "gh-arguments")
	t.Setenv("GH_ARGUMENTS", arguments)
	testutil.WithFakeExecutables(t, map[string]string{
		"gh": `printf '%s\n' "$*" >> "$GH_ARGUMENTS"; echo 'synthetic refusal' >&2; exit 1`,
	})
	err := Client{Runner: subprocess.ExecRunner{}}.UpdateComment(context.Background(), "IC_synthetic", "synthetic private body")
	if err == nil {
		t.Fatal("UpdateComment() succeeded against a failing gh")
	}
	if strings.Contains(err.Error(), "synthetic private body") || !strings.Contains(err.Error(), "updateIssueComment") {
		t.Errorf("error = %q, want the mutation named and the body omitted", err)
	}
	if called, readErr := os.ReadFile(arguments); readErr != nil || !strings.Contains(string(called), "api graphql") {
		t.Errorf("gh was not invoked as expected: %q, %v", called, readErr)
	}
}

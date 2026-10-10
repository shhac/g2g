package githubstack

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
)

// appliedJSON is GitHub's answer to a document whose writes all applied.
func appliedJSON(count int) string {
	aliases := make([]string, 0, count)
	for index := 0; index < count; index++ {
		aliases = append(aliases, fmt.Sprintf(`"w%d":{"clientMutationId":null}`, index))
	}
	return `{"data":{` + strings.Join(aliases, ",") + `}}`
}

func argument(argv []string, key string) (string, bool) {
	for index := 0; index+1 < len(argv); index++ {
		if argv[index] != "-f" {
			continue
		}
		if value, found := strings.CutPrefix(argv[index+1], key+"="); found {
			return value, true
		}
	}
	return "", false
}

// Adding one comment and editing another is one request, and each body is
// posted exactly as written: a raw field, never a typed one that would read a
// file for a leading @ or fill in {owner}.
func TestWriteCommentsSendsEveryWriteInOneDocument(t *testing.T) {
	body := "@synthetic-file {owner} body\nsecond line"
	runner := &scriptedRunner{responses: []string{appliedJSON(2)}}
	outcomes, err := Client{Runner: runner}.WriteComments(context.Background(), []CommentWrite{
		{Subject: "PR_synthetic_11", Body: body},
		{Comment: "IC_synthetic_12", Body: body + " edited"},
	})
	if err != nil || len(outcomes) != 2 || outcomes[0] != nil || outcomes[1] != nil {
		t.Fatalf("WriteComments() = %v, %v, want both applied", outcomes, err)
	}
	if len(runner.argv) != 1 {
		t.Fatalf("%d requests, want one: %q", len(runner.argv), runner.argv)
	}
	argv := runner.argv[0]
	if strings.Join(argv[:3], " ") != "gh api graphql" {
		t.Errorf("argv = %q, want gh api graphql", argv)
	}
	query, _ := argument(argv, "query")
	for _, want := range []string{
		"mutation WriteStackComments(",
		"w0: addComment(input: {subjectId: $subject0, body: $body0})",
		"w1: updateIssueComment(input: {id: $comment1, body: $body1})",
	} {
		if !strings.Contains(query, want) {
			t.Errorf("document is missing %q:\n%s", want, query)
		}
	}
	if strings.Contains(query, "synthetic") {
		t.Errorf("a value reached the document's text:\n%s", query)
	}
	for key, want := range map[string]string{"subject0": "PR_synthetic_11", "body0": body, "comment1": "IC_synthetic_12", "body1": body + " edited"} {
		if got, found := argument(argv, key); !found || got != want {
			t.Errorf("-f %s = %q, want %q", key, got, want)
		}
	}
	if strings.Contains(strings.Join(argv, "\x00"), "-F\x00body") {
		t.Errorf("a body went as a typed field: %q", argv)
	}
}

// gh exits non-zero when any write in the document failed, and GitHub went on
// to apply the others. Each write gets its own answer, in GitHub's words, and
// the call as a whole did not fail.
func TestARefusedWriteFailsOnlyItself(t *testing.T) {
	response := `{"data":{"w0":{"clientMutationId":null},"w1":null,"w2":{"clientMutationId":null}},` +
		`"errors":[{"type":"FORBIDDEN","path":["w1"],"message":"synthetic refusal of the second"}]}`
	runner := &scriptedRunner{responses: []string{response}, failing: map[int]bool{0: true}}
	outcomes, err := Client{Runner: runner}.Retarget(context.Background(), []BaseChange{
		{ID: "PR_synthetic_11", Number: 11, Base: "synthetic-trunk"},
		{ID: "PR_synthetic_12", Number: 12, Base: "synthetic-trunk"},
		{ID: "PR_synthetic_13", Number: 13, Base: "synthetic-trunk"},
	})
	if err != nil {
		t.Fatalf("Retarget() whole error = %v, want only the one write failed", err)
	}
	if outcomes[0] != nil || outcomes[2] != nil {
		t.Errorf("outcomes = %v, want the first and third applied", outcomes)
	}
	if outcomes[1] == nil || outcomes[1].Error() != "synthetic refusal of the second" {
		t.Errorf("second outcome = %v, want GitHub's own words", outcomes[1])
	}
}

// An alias GitHub answered with nothing, and no error naming it, is not taken
// as applied: success is only what the answer says happened.
func TestAnUnansweredWriteIsNotTakenAsApplied(t *testing.T) {
	runner := &scriptedRunner{responses: []string{`{"data":{"w0":{"clientMutationId":null}}}`}}
	outcomes, err := Client{Runner: runner}.WriteComments(context.Background(), []CommentWrite{
		{Subject: "PR_synthetic_11", Body: "synthetic body"},
		{Subject: "PR_synthetic_12", Body: "synthetic body"},
	})
	if err != nil || outcomes[0] != nil || outcomes[1] == nil {
		t.Fatalf("WriteComments() = %v, %v, want the second reported as not applied", outcomes, err)
	}
}

// Output that is not an answer at all — gh never reached GitHub, or was not
// signed in — fails the whole document as the command that failed, so the
// remediation advice that matches on "gh " still recognises it, and every
// write carries the same reason.
func TestAnUnreadableAnswerFailsTheWholeDocument(t *testing.T) {
	for name, response := range map[string]string{
		"not JSON":     "synthetic: not signed in",
		"no data":      `{"errors":[{"type":"RATE_LIMITED","message":"synthetic rate limit exceeded"}]}`,
		"data is null": `{"data":null,"errors":[{"message":"synthetic parse error"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			runner := &scriptedRunner{responses: []string{response}, failing: map[int]bool{0: true}}
			outcomes, err := Client{Runner: runner}.WriteComments(context.Background(), []CommentWrite{
				{Subject: "PR_synthetic_11", Body: "synthetic private body"},
				{Comment: "IC_synthetic_12", Body: "synthetic private body"},
			})
			var command *CommandError
			if !errors.As(err, &command) || !strings.HasPrefix(command.Command, "gh ") {
				t.Fatalf("error = %#v, want a command error naming gh", err)
			}
			if !strings.Contains(command.Command, "WriteStackComments") {
				t.Errorf("command = %q, want the operation named", command.Command)
			}
			for index, outcome := range outcomes {
				if outcome != err {
					t.Errorf("outcome %d = %v, want the whole document's failure", index, outcome)
				}
			}
			if strings.Contains(err.Error(), "synthetic private body") {
				t.Errorf("error repeats a body: %v", err)
			}
		})
	}
	runner := &scriptedRunner{responses: []string{`{"errors":[{"type":"RATE_LIMITED","message":"API rate limit exceeded"}]}`}, failing: map[int]bool{0: true}}
	_, err := Client{Runner: runner}.Retarget(context.Background(), []BaseChange{{ID: "PR_synthetic_11", Number: 11, Base: "synthetic-trunk"}})
	if !RateLimited(err) {
		t.Errorf("RateLimited(%v) = false, want a refused document recognised as a rate limit", err)
	}
}

// Twenty writes to a document, sent in order. A refusal inside one document
// does not stop the next, because GitHub applied everything else in it; a
// document that failed as a whole does, because nothing says what it did.
func TestWritesAreChunkedAndSentInOrder(t *testing.T) {
	changes := make([]BaseChange, 0, 45)
	for number := 1; number <= 45; number++ {
		changes = append(changes, BaseChange{ID: fmt.Sprintf("PR_synthetic_%d", number), Number: number, Base: "synthetic-trunk"})
	}
	refused := `{"data":{` + strings.TrimSuffix(strings.TrimPrefix(appliedJSON(20), `{"data":{`), `}}`) + `,"w3":null},"errors":[{"path":["w3"],"message":"synthetic refusal"}]}`
	refused = strings.Replace(refused, `"w3":{"clientMutationId":null},`, ``, 1)

	runner := &scriptedRunner{responses: []string{appliedJSON(20), refused, appliedJSON(5)}, failing: map[int]bool{1: true}}
	outcomes, err := Client{Runner: runner}.Retarget(context.Background(), changes)
	if err != nil {
		t.Fatalf("Retarget() whole error = %v", err)
	}
	if len(runner.argv) != 3 {
		t.Fatalf("%d requests, want three for 45 writes", len(runner.argv))
	}
	for index, first := range []string{"PR_synthetic_1", "PR_synthetic_21", "PR_synthetic_41"} {
		if got, _ := argument(runner.argv[index], "pullRequest0"); got != first {
			t.Errorf("request %d starts at %q, want %q", index, got, first)
		}
	}
	if got, _ := argument(runner.argv[2], "pullRequest4"); got != "PR_synthetic_45" {
		t.Errorf("the last request ends at %q, want PR_synthetic_45", got)
	}
	for index, outcome := range outcomes {
		if (outcome != nil) != (index == 23) {
			t.Errorf("outcome %d = %v, want only #24 refused", index, outcome)
		}
	}

	stopped := &scriptedRunner{responses: []string{appliedJSON(20), "synthetic: connection reset"}, failing: map[int]bool{1: true}}
	outcomes, err = Client{Runner: stopped}.Retarget(context.Background(), changes)
	if err == nil {
		t.Fatal("Retarget() = nil, want the failed document reported")
	}
	if len(stopped.argv) != 2 {
		t.Errorf("%d requests, want none after the one that failed", len(stopped.argv))
	}
	for index, outcome := range outcomes {
		if (outcome == nil) != (index < 20) {
			t.Errorf("outcome %d = %v, want the first document applied and nothing after it", index, outcome)
		}
	}
}

// Each refusal holds before gh is invoked, and refuses the whole call: a
// change read without its node id is named, never skipped.
func TestRetargetRefusesBeforeInvokingGh(t *testing.T) {
	for _, test := range []struct {
		name    string
		client  Client
		change  BaseChange
		wantErr string
	}{
		{name: "no runner", client: Client{}, change: BaseChange{ID: "PR_synthetic_7", Number: 7, Base: "synthetic-trunk"}, wantErr: "runner is not configured"},
		{name: "no number", client: Client{Runner: refusingRunner{t: t}}, change: BaseChange{ID: "PR_synthetic_7", Base: "synthetic-trunk"}, wantErr: "number is required"},
		{name: "no node id", client: Client{Runner: refusingRunner{t: t}}, change: BaseChange{Number: 7, Base: "synthetic-trunk"}, wantErr: "#7"},
		{name: "option-like base", client: Client{Runner: refusingRunner{t: t}}, change: BaseChange{ID: "PR_synthetic_7", Number: 7, Base: "--synthetic-option"}, wantErr: "base branch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			outcomes, err := test.client.Retarget(context.Background(), []BaseChange{{ID: "PR_synthetic_6", Number: 6, Base: "synthetic-trunk"}, test.change})
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Retarget() = %v, want it to mention %q", err, test.wantErr)
			}
			if len(outcomes) != 2 || outcomes[0] == nil || outcomes[1] == nil {
				t.Errorf("outcomes = %v, want every write refused", outcomes)
			}
		})
	}
}

func TestWriteCommentsRefusesAMalformedWrite(t *testing.T) {
	for name, write := range map[string]CommentWrite{
		"no body":      {Subject: "PR_synthetic_7"},
		"neither":      {Body: "synthetic body"},
		"both targets": {Subject: "PR_synthetic_7", Comment: "IC_synthetic_7", Body: "synthetic body"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := (Client{Runner: refusingRunner{t: t}}).WriteComments(context.Background(), []CommentWrite{write}); err == nil {
				t.Fatal("WriteComments() = nil, want a refusal")
			}
		})
	}
}

// A failed document names the operation and never repeats a body back.
func TestAFailedDocumentDoesNotEchoTheBody(t *testing.T) {
	arguments := filepath.Join(t.TempDir(), "gh-arguments")
	t.Setenv("GH_ARGUMENTS", arguments)
	testutil.WithFakeExecutables(t, map[string]string{
		"gh": `printf '%s\n' "$*" >> "$GH_ARGUMENTS"; echo 'synthetic refusal' >&2; exit 1`,
	})
	_, err := Client{Runner: subprocess.ExecRunner{}}.WriteComments(context.Background(), []CommentWrite{{Comment: "IC_synthetic", Body: "synthetic private body"}})
	if err == nil {
		t.Fatal("WriteComments() succeeded against a failing gh")
	}
	if strings.Contains(err.Error(), "synthetic private body") || !strings.Contains(err.Error(), "WriteStackComments") {
		t.Errorf("error = %q, want the operation named and the body omitted", err)
	}
	if called, readErr := os.ReadFile(arguments); readErr != nil || !strings.Contains(string(called), "api graphql") {
		t.Errorf("gh was not invoked as expected: %q, %v", called, readErr)
	}
}

func TestDescribeUnappliedNamesASharedReasonOnce(t *testing.T) {
	shared, other := errors.New("synthetic shared reason"), errors.New("synthetic other reason")
	got := DescribeUnapplied([]Unapplied{{Number: 12, Err: shared}, {Number: 13, Err: other}, {Number: 14, Err: shared}})
	if want := "#12 and #14: synthetic shared reason; #13: synthetic other reason"; got != want {
		t.Errorf("DescribeUnapplied() = %q, want %q", got, want)
	}
}

// refusingRunner fails the test if anything reaches it, which is what makes a
// refusal test assert the refusal happened *before* the call rather than that
// the call happened to fail.
type refusingRunner struct{ t *testing.T }

func (r refusingRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.t.Helper()
	r.t.Errorf("a refused write still invoked %s %s", name, strings.Join(args, " "))
	return nil, nil
}

package githubstack

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/diagnostic"
	"github.com/shhac/gt2gh/internal/subprocess"
	"github.com/shhac/gt2gh/internal/testutil"
)

func TestClientUsesFakeGitHubCLIOnPATH(t *testing.T) {
	arguments := filepath.Join(t.TempDir(), "gh-arguments")
	t.Setenv("GH_ARGUMENTS", arguments)
	testutil.WithFakeExecutables(t, map[string]string{
		"gh": `printf '%s\n' "$*" >> "$GH_ARGUMENTS"
if [ "$1 $2" = "repo view" ]; then printf '{"nameWithOwner":"example/fixture"}\n'; fi
if [ "$1 $2 $3" = "api graphql -f" ]; then printf '{"data":{"pr0":{"nodes":[{"number":4,"url":"https://example.test/4","headRefName":"alpha","baseRefName":"main","state":"OPEN"}]}}}\n'; fi`,
	})
	client := Client{Runner: subprocess.ExecRunner{}}
	prs, err := client.Inspect(context.Background(), []string{"alpha"})
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if len(prs) != 1 || prs[0].Head != "alpha" {
		t.Fatalf("Inspect() = %#v", prs)
	}
	if err := client.Link(context.Background(), "main", []string{"alpha", "beta"}); err != nil {
		t.Fatalf("Link() error = %v", err)
	}
	called, err := os.ReadFile(arguments)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(called), "repo view --json nameWithOwner\n") || !strings.Contains(string(called), "api graphql -f query=query { pr0: search(") || !strings.Contains(string(called), "stack { number size } stackEntry { position }") || !strings.Contains(string(called), "stack link --base main alpha beta\n") {
		t.Errorf("unexpected gh calls: %q", called)
	}
}

func TestInspectDebugSummarizesGraphQLWithoutLoggingQuery(t *testing.T) {
	testutil.WithFakeExecutables(t, map[string]string{
		"gh": `if [ "$1 $2" = "repo view" ]; then printf '{"nameWithOwner":"example/synthetic"}\n'; fi
if [ "$1 $2 $3" = "api graphql -f" ]; then printf '{"data":{"pr0":{"nodes":[{"number":7,"headRefName":"synthetic-head","baseRefName":"synthetic-base","state":"OPEN"}]}}}\n'; fi`,
	})
	var diagnostics bytes.Buffer
	ctx := diagnostic.WithSink(context.Background(), diagnostic.Writer{Out: &diagnostics})
	client := Client{Runner: subprocess.ObservingRunner{Runner: subprocess.ExecRunner{}}}
	if _, err := client.Inspect(ctx, []string{"synthetic-head"}); err != nil {
		t.Fatal(err)
	}
	got := diagnostics.String()
	for _, expected := range []string{"event=github.query", "kind=\"batched_pull_requests\"", "query=\"omitted\"", "event=github.pull_request", "head=\"synthetic-head\"", "command=\"gh api graphql query=omitted\""} {
		if !strings.Contains(got, expected) {
			t.Errorf("diagnostics missing %q: %q", expected, got)
		}
	}
	if strings.Contains(got, "query=query {") {
		t.Errorf("diagnostics leaked GraphQL payload: %q", got)
	}
}

func TestInspectRejectsInvalidJSON(t *testing.T) {
	testutil.WithFakeExecutables(t, map[string]string{"gh": "printf 'not json\\n'"})
	_, err := (Client{Runner: subprocess.ExecRunner{}}).Inspect(context.Background(), []string{"alpha"})
	if err == nil || !strings.Contains(err.Error(), "parse gh repo view JSON") {
		t.Fatalf("Inspect() error = %v", err)
	}
}

func TestParsePullRequestsRejectsGraphQLFailures(t *testing.T) {
	for _, output := range []string{
		`{"errors":[{"message":"synthetic failure"}]}`,
		`{"data":{}}`,
		`{"data":{"pr0":{"nodes":[]}}}`,
	} {
		if _, err := parsePullRequests([]byte(output), []string{"alpha", "beta"}); err == nil {
			t.Errorf("parsePullRequests(%s) error = nil", output)
		}
	}
}

func TestParsePullRequestsRejectsInvalidNode(t *testing.T) {
	output := []byte(`{"data":{"pr0":{"nodes":[{"number":0,"headRefName":"alpha","baseRefName":"main","state":"OPEN"}]}}}`)
	if _, err := parsePullRequests(output, []string{"alpha"}); err == nil {
		t.Fatal("parsePullRequests() error = nil")
	}
}

func TestParsePullRequestsPreservesNativeStackMembership(t *testing.T) {
	output := []byte(`{"data":{"pr0":{"nodes":[{"number":17,"url":"https://example.test/17","headRefName":"synthetic/top","baseRefName":"synthetic/lower","state":"OPEN","stack":{"number":8,"size":3},"stackEntry":{"position":2}}]}}}`)
	prs, err := parsePullRequests(output, []string{"synthetic/top"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := prs[0].StackNumber, 8; got != want {
		t.Errorf("StackNumber = %d, want %d", got, want)
	}
	if got, want := prs[0].StackSize, 3; got != want {
		t.Errorf("StackSize = %d, want %d", got, want)
	}
	if got, want := prs[0].StackPosition, 2; got != want {
		t.Errorf("StackPosition = %d, want %d", got, want)
	}
}

func TestParsePullRequestsRejectsIncompleteNativeStackMembership(t *testing.T) {
	output := []byte(`{"data":{"pr0":{"nodes":[{"number":17,"headRefName":"synthetic/top","baseRefName":"synthetic/lower","state":"OPEN","stack":{"number":8,"size":3},"stackEntry":null}]}}}`)
	if _, err := parsePullRequests(output, []string{"synthetic/top"}); err == nil {
		t.Fatal("parsePullRequests() error = nil")
	}
}

func TestBoundedMessage(t *testing.T) {
	if got := boundedMessage(strings.Repeat("x", 501)); !strings.HasSuffix(got, "…") || len(got) != 503 {
		t.Errorf("boundedMessage() = %q", got)
	}
}

func TestClientLinkSeparatesSummaryFromSyntheticDiagnostic(t *testing.T) {
	testutil.WithFakeExecutables(t, map[string]string{
		"gh": `printf '%s\n' '! [rejected] synthetic-a -> synthetic-a (non-fast-forward)' >&2
printf '%s\n' 'error: failed to push synthetic refs' >&2
exit 1`,
	})
	err := (Client{Runner: subprocess.ExecRunner{}}).Link(context.Background(), "main", []string{"alpha", "beta"})
	var commandErr *CommandError
	if !errors.As(err, &commandErr) {
		t.Fatalf("Link() error = %v, want CommandError", err)
	}
	if got, want := commandErr.Summary(), "gh stack link failed."; got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
	if got, want := commandErr.Diagnostic(), "! [rejected] synthetic-a -> synthetic-a (non-fast-forward)\nerror: failed to push synthetic refs"; got != want {
		t.Errorf("diagnostic = %q, want %q", got, want)
	}
}

func TestClientCreatePassesDraftReviewersAndRemovesBodyFile(t *testing.T) {
	arguments := filepath.Join(t.TempDir(), "gh-arguments")
	bodyPath := filepath.Join(t.TempDir(), "body-path")
	t.Setenv("GH_ARGUMENTS", arguments)
	t.Setenv("GH_BODY_PATH", bodyPath)
	testutil.WithFakeExecutables(t, map[string]string{
		"gh": `printf '%s\n' "$*" > "$GH_ARGUMENTS"
for arg in "$@"; do
  case "$arg" in *g2g-submit-body-*.md) printf '%s' "$arg" > "$GH_BODY_PATH";; esac
done`,
	})
	client := Client{Runner: subprocess.ExecRunner{}}
	if err := client.Create(context.Background(), "synthetic/head", "synthetic/base", "Synthetic title", "# synthetic body", true, []string{"reviewer-one", "reviewer-two"}); err != nil {
		t.Fatal(err)
	}
	argumentsBytes, err := os.ReadFile(arguments)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"pr create", "--head synthetic/head", "--base synthetic/base", "--title Synthetic title", "--body-file", "--draft", "--reviewer reviewer-one", "--reviewer reviewer-two"} {
		if !strings.Contains(string(argumentsBytes), want) {
			t.Errorf("arguments missing %q: %q", want, argumentsBytes)
		}
	}
	path, err := os.ReadFile(bodyPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(string(path)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("body file %q still exists or could not be checked: %v", path, err)
	}
}

func TestClientCreateRejectsIncompleteRequestBeforeGh(t *testing.T) {
	called := filepath.Join(t.TempDir(), "called")
	t.Setenv("GH_CALLED", called)
	testutil.WithFakeExecutables(t, map[string]string{"gh": `touch "$GH_CALLED"`})
	err := (Client{Runner: subprocess.ExecRunner{}}).Create(context.Background(), "", "synthetic/base", "Synthetic title", "", false, nil)
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := os.Stat(called); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("gh was called for an invalid request")
	}
}

func TestClientUnstackValidatesAndInvokesExpectedCommand(t *testing.T) {
	arguments := filepath.Join(t.TempDir(), "gh-arguments")
	t.Setenv("GH_ARGUMENTS", arguments)
	testutil.WithFakeExecutables(t, map[string]string{"gh": `printf '%s' "$*" > "$GH_ARGUMENTS"`})
	client := Client{Runner: subprocess.ExecRunner{}}
	if err := client.Unstack(context.Background(), 0); err == nil || !strings.Contains(err.Error(), "positive") {
		t.Fatalf("Unstack(0) error = %v", err)
	}
	if _, err := os.Stat(arguments); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("gh was called for an invalid stack number")
	}
	if err := client.Unstack(context.Background(), 23); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(arguments)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "stack unstack 23" {
		t.Errorf("arguments = %q", got)
	}
}

func TestClientCreateRemovesBodyFileAfterFailure(t *testing.T) {
	bodyPath := filepath.Join(t.TempDir(), "body-path")
	t.Setenv("GH_BODY_PATH", bodyPath)
	testutil.WithFakeExecutables(t, map[string]string{
		"gh": `for arg in "$@"; do
  case "$arg" in *g2g-submit-body-*.md) printf '%s' "$arg" > "$GH_BODY_PATH";; esac
done
printf '%s\n' 'synthetic create rejected' >&2
exit 1`,
	})
	err := (Client{Runner: subprocess.ExecRunner{}}).Create(context.Background(), "synthetic/head", "synthetic/base", "Synthetic title", "body", false, nil)
	if err == nil {
		t.Fatal("Create() error = nil")
	}
	path, readErr := os.ReadFile(bodyPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if _, statErr := os.Stat(string(path)); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("body file %q still exists or could not be checked: %v", path, statErr)
	}
}

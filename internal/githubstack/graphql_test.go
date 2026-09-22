package githubstack

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// recordingRunner answers one GraphQL call and keeps its argv, because the
// question is what reached gh rather than what came back.
type recordingRunner struct {
	output []byte
	args   []string
}

func (r *recordingRunner) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	r.args = args
	return r.output, nil
}

// Go's quoting writes U+E0041 as \U000e0041, which GraphQL has no escape for,
// so a single such branch failed the whole batch. Sent as variables, the names
// never reach the query text and gh passes each through untouched.
func TestInspectSendsHeadsAsVariablesNotQueryText(t *testing.T) {
	branches := []string{"synthetic-plain", "synthetic-tag\U000E0041", `synthetic-"quoted"\slash`, "synthetic-{owner}", "@synthetic-file"}
	nodes := make([]string, 0, len(branches))
	for index, branch := range branches {
		head, err := json.Marshal(branch)
		if err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, fmt.Sprintf(`"pr%d":{"nodes":[{"number":%d,"headRefName":%s,"baseRefName":"synthetic-trunk","state":"OPEN"}]}`, index, index+1, head))
	}
	runner := &recordingRunner{output: []byte(`{"data":{"repository":{` + strings.Join(nodes, ",") + `}}}`)}

	prs, err := (Client{Runner: runner}).Inspect(context.Background(), branches)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if len(prs) != len(branches) {
		t.Fatalf("Inspect() = %d pull requests, want %d: %#v", len(prs), len(branches), prs)
	}

	var query string
	heads := map[string]string{}
	for index := 0; index+1 < len(runner.args); index++ {
		if runner.args[index] != "-f" {
			continue
		}
		key, value, _ := strings.Cut(runner.args[index+1], "=")
		if key == "query" {
			query = value
			continue
		}
		heads[key] = value
	}
	for index, branch := range branches {
		key := fmt.Sprintf("head%d", index)
		if heads[key] != branch {
			t.Errorf("%s = %q, want %q", key, heads[key], branch)
		}
		if !strings.Contains(query, fmt.Sprintf("$%s: String!", key)) || !strings.Contains(query, fmt.Sprintf("headRefName: $%s,", key)) {
			t.Errorf("query does not declare and use $%s:\n%s", key, query)
		}
	}
	for _, branch := range branches[1:] {
		if strings.Contains(query, branch) {
			t.Errorf("query text carries branch %q:\n%s", branch, query)
		}
	}
	if strings.Contains(query, `\U`) || strings.Contains(query, `\x`) {
		t.Errorf("query carries an escape GraphQL does not have:\n%s", query)
	}
}

// headRefName matches by name alone, so a fork with a branch of the same name
// answers too. Counting it put an open pull request on a branch that has none.
func TestParsePullRequestsIgnoresForkHeads(t *testing.T) {
	output := []byte(`{"data":{"repository":{` +
		`"pr0":{"nodes":[{"number":9,"headRefName":"synthetic-shared","baseRefName":"synthetic-trunk","state":"OPEN","isCrossRepository":true},{"number":3,"headRefName":"synthetic-shared","baseRefName":"synthetic-trunk","state":"OPEN","isCrossRepository":false}]},` +
		`"pr1":{"nodes":[{"number":11,"headRefName":"synthetic-unpublished","baseRefName":"synthetic-trunk","state":"OPEN","isCrossRepository":true}]}}}}`)

	prs, err := parsePullRequests(output, []string{"synthetic-shared", "synthetic-unpublished"})
	if err != nil {
		t.Fatalf("parsePullRequests() error = %v", err)
	}
	if len(prs) != 1 || prs[0].Number != 3 {
		t.Fatalf("parsePullRequests() = %#v, want only #3; a fork's pull request is not this branch's", prs)
	}
}

func TestGraphQLStringWritesOnlyEscapesGraphQLHas(t *testing.T) {
	for input, want := range map[string]string{
		"synthetic-plain":          `"synthetic-plain"`,
		`synthetic-"quoted"`:       `"synthetic-\"quoted\""`,
		`synthetic\slash`:          `"synthetic\\slash"`,
		"synthetic\nline\ttab\x7f": `"synthetic\u000Aline\u0009tab\u007F"`,
		"synthetic-é":              `"synthetic-\u00E9"`,
		"synthetic-\U000E0041":     `"synthetic-\uDB40\uDC41"`,
		"":                         `""`,
		"synthetic-cursor==/+":     `"synthetic-cursor==/+"`,
	} {
		if got := graphqlString(input); got != want {
			t.Errorf("graphqlString(%q) = %s, want %s", input, got, want)
		}
	}
}

// Every escape graphqlString writes is also one JSON has, with the same
// meaning, so decoding its output as JSON must give back the input. This is
// what catches an escape that looks right and means something else.
func TestGraphQLStringRoundTripsThroughAJSONDecoder(t *testing.T) {
	for _, input := range []string{"synthetic-\U000E0041\U0001F600", "synthetic-\x00\x1f", `"\`, "synthetic-ünïcødé"} {
		var decoded string
		if err := json.Unmarshal([]byte(graphqlString(input)), &decoded); err != nil {
			t.Fatalf("graphqlString(%q) = %s is not a JSON string: %v", input, graphqlString(input), err)
		}
		if decoded != input {
			t.Errorf("graphqlString(%q) decodes to %q", input, decoded)
		}
	}
}

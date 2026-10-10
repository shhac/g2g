// Writing to pull requests, many at a time.
//
// Kept apart from the reads for the reason inspect.go gives: what a mutation
// sends and how its answer is read change for different reasons than a query.
package githubstack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/words"
)

// mutationsPerDocument bounds how many writes ride in one request. A stack's
// comments and bases were a request each, which is what spends an account's
// allowance on a long stack; twenty keeps the argument list well inside what a
// process may be handed, with every comment body in it, and each request well
// inside the ten seconds GitHub gives one before giving up on it.
const mutationsPerDocument = 20

// CommentWrite is one stack comment to send: a new one on the pull request
// whose node id is Subject, or a replacement body for the comment whose node
// id is Comment. Exactly one of the two is set.
type CommentWrite struct {
	Subject string
	Comment string
	Body    string
}

// WriteComments adds and edits stack comments, as few requests as it takes.
//
// The body travels as a raw field, which gh passes through as a string: it
// reads no file for a value starting with @ and fills no {owner} placeholder,
// so a body is posted exactly as written. What it returns is described on
// mutate.
func (c Client) WriteComments(ctx context.Context, writes []CommentWrite) ([]error, error) {
	fields := make([]mutationField, 0, len(writes))
	for _, write := range writes {
		if write.Body == "" || (write.Subject == "") == (write.Comment == "") {
			return refuseAll(len(writes), fmt.Errorf("a comment write needs a body and exactly one of a pull request or a comment"))
		}
		if write.Subject != "" {
			fields = append(fields, mutationField{name: "addComment", inputs: []mutationInput{
				{field: "subjectId", variable: "subject", kind: "ID!", value: write.Subject},
				{field: "body", variable: "body", kind: "String!", value: write.Body},
			}})
			continue
		}
		fields = append(fields, mutationField{name: "updateIssueComment", inputs: []mutationInput{
			{field: "id", variable: "comment", kind: "ID!", value: write.Comment},
			{field: "body", variable: "body", kind: "String!", value: write.Body},
		}})
	}
	return c.mutate(ctx, "WriteStackComments", fields)
}

// BaseChange points one open pull request at a different base branch. ID is
// its node id, which the mutation addresses; Number names it to a person.
type BaseChange struct {
	ID     string
	Number int
	Base   string
}

// Retarget moves pull requests' bases, as few requests as it takes.
//
// It is the one GitHub mutation that changes what a merge will do rather than
// how the pull requests are displayed, which is why it is a deliberate command
// of its own rather than the tail of a restack. A change read without its node
// id — a pull request only remembered, never read — refuses the whole call
// before anything is sent, naming it, rather than being skipped. What it
// returns is described on mutate.
func (c Client) Retarget(ctx context.Context, changes []BaseChange) ([]error, error) {
	fields := make([]mutationField, 0, len(changes))
	for _, change := range changes {
		if change.Number <= 0 {
			return refuseAll(len(changes), fmt.Errorf("pull request number is required"))
		}
		if change.ID == "" {
			return refuseAll(len(changes), fmt.Errorf("#%d was read without its node id, so its base cannot be changed · rerun to read it again", change.Number))
		}
		if err := subprocess.CheckArgument("gh", "base branch", change.Base); err != nil {
			return refuseAll(len(changes), err)
		}
		fields = append(fields, mutationField{name: "updatePullRequest", inputs: []mutationInput{
			{field: "pullRequestId", variable: "pullRequest", kind: "ID!", value: change.ID},
			{field: "baseRefName", variable: "base", kind: "String!", value: change.Base},
		}})
	}
	return c.mutate(ctx, "RetargetPullRequests", fields)
}

// mutationField is one write: the mutation it calls and the input it passes.
type mutationField struct {
	name   string
	inputs []mutationInput
}

// mutationInput is one field of a mutation's input. Its value travels as a
// variable named for the stem and the write's position, never in the
// document's text, for the reason headVariables gives.
type mutationInput struct {
	field    string
	variable string
	kind     string
	value    string
}

// mutate sends writes as documents of aliased mutations, at most
// mutationsPerDocument to a document, in order.
//
// The answer has one entry per write: nil for a write GitHub applied, and why
// for one it did not. GitHub runs every mutation in a document, one after
// another in the order written, whatever happened to the one before — so a
// refusal fails its own write and no other, and the documents after it are
// still sent. A document that fails as a whole — gh could not reach GitHub,
// was not signed in, or GitHub refused the request itself — stops there:
// that error is the second return, and it is also the entry of every write
// from that document on, none of which is known to have been applied.
func (c Client) mutate(ctx context.Context, operation string, fields []mutationField) ([]error, error) {
	if c.Runner == nil {
		return refuseAll(len(fields), fmt.Errorf("GitHub runner is not configured"))
	}
	outcomes := make([]error, len(fields))
	for start := 0; start < len(fields); start += mutationsPerDocument {
		chunk := fields[start:min(start+mutationsPerDocument, len(fields))]
		diagnostic.Event(ctx, "github.mutation", diagnostic.Field{Key: "kind", Value: operation}, diagnostic.Field{Key: "writes", Value: strconv.Itoa(len(chunk))})
		output, err := c.Runner.Run(ctx, "gh", mutationArguments(operation, chunk)...)
		applied, whole := mutationOutcomes(output, err, "gh api graphql "+operation+" …", len(chunk))
		if whole != nil {
			for index := start; index < len(fields); index++ {
				outcomes[index] = whole
			}
			return outcomes, whole
		}
		copy(outcomes[start:], applied)
	}
	return outcomes, nil
}

// refuseAll is a call refused before anything was sent: every write carries
// the reason, as mutate's answer would.
func refuseAll(count int, err error) ([]error, error) {
	outcomes := make([]error, count)
	for index := range outcomes {
		outcomes[index] = err
	}
	return outcomes, err
}

// mutationArguments is gh's argument list for one document: the writes
// aliased w0, w1, … in order, and every value as a raw -f variable.
func mutationArguments(operation string, chunk []mutationField) []string {
	declarations := make([]string, 0, 2*len(chunk))
	selections := make([]string, 0, len(chunk))
	variables := make([]string, 0, 4*len(chunk))
	for index, field := range chunk {
		inputs := make([]string, 0, len(field.inputs))
		for _, input := range field.inputs {
			name := input.variable + strconv.Itoa(index)
			declarations = append(declarations, "$"+name+": "+input.kind)
			inputs = append(inputs, input.field+": $"+name)
			variables = append(variables, "-f", name+"="+input.value)
		}
		selections = append(selections, fmt.Sprintf("w%d: %s(input: {%s}) { clientMutationId }", index, field.name, strings.Join(inputs, ", ")))
	}
	document := fmt.Sprintf("mutation %s(%s) { %s }", operation, strings.Join(declarations, ", "), strings.Join(selections, " "))
	return append([]string{"api", "graphql", "-f", "query=" + document}, variables...)
}

// mutationResponse is a mutation's answer: each write's payload keyed by its
// alias at the top level, null for one that failed, and the errors that say
// why, each naming its alias first in its path.
type mutationResponse struct {
	Data   map[string]json.RawMessage `json:"data"`
	Errors []graphqlError             `json:"errors"`
}

// mutationOutcomes reads one document's answer.
//
// gh exits non-zero whenever the answer carries an error, a refused write
// among them, so the exit status alone cannot say whether anything was
// applied; the answer can. Only an answer with no data in it at all is a
// failure of the whole document, and that is reported as the command that
// failed, so the authentication and rate limit advice still recognises it.
func mutationOutcomes(output []byte, runErr error, display string, count int) ([]error, error) {
	var response mutationResponse
	parseErr := decodeFirstJSON(output, &response)
	if parseErr != nil || response.Data == nil {
		cause := runErr
		if cause == nil {
			cause = errors.New("GitHub answered with nothing it applied")
		}
		return nil, commandError(display, cause, output)
	}
	outcomes := make([]error, count)
	for index := range outcomes {
		alias := "w" + strconv.Itoa(index)
		if payload, found := response.Data[alias]; found && string(payload) != "null" {
			continue
		}
		outcomes[index] = refusal(alias, response.Errors)
	}
	return outcomes, nil
}

// refusal is why GitHub did not apply one alias, in its own words where it
// gave any, bounded and redacted as every command's output is.
func refusal(alias string, failures []graphqlError) error {
	reasons := make([]string, 0, 1)
	for _, failure := range failures {
		if len(failure.Path) == 0 || failure.Path[0] != alias || failure.Message == "" {
			continue
		}
		reasons = append(reasons, failure.Message)
	}
	if len(reasons) == 0 {
		return errors.New("GitHub did not apply it and said nothing about why")
	}
	return errors.New(diagnostic.BoundedOutput([]byte(strings.Join(reasons, "; "))))
}

// Unapplied is one write of a batch that was not applied: the pull request it
// was for, and why.
type Unapplied struct {
	Number int
	Err    error
}

// DescribeUnapplied says which writes were not applied and why, naming the
// pull requests that share a reason together, in the order they came: "#12
// and #13: why; #15: why else". A document that failed as a whole fails each
// of its writes for the same reason, and repeating it once per pull request
// would bury the one line worth reading.
func DescribeUnapplied(unapplied []Unapplied) string {
	reasons := make([]string, 0, len(unapplied))
	numbers := map[string][]string{}
	for _, write := range unapplied {
		reason := write.Err.Error()
		if _, seen := numbers[reason]; !seen {
			reasons = append(reasons, reason)
		}
		numbers[reason] = append(numbers[reason], "#"+strconv.Itoa(write.Number))
	}
	parts := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		parts = append(parts, words.List(numbers[reason])+": "+reason)
	}
	return strings.Join(parts, "; ")
}

// NamePullRequests names pull requests as a sentence does: "#12 and #13".
func NamePullRequests(numbers []int) string {
	named := make([]string, 0, len(numbers))
	for _, number := range numbers {
		named = append(named, "#"+strconv.Itoa(number))
	}
	return words.List(named)
}

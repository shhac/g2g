// Package githubstack provides read-only PR inspection and the explicit
// GitHub stack-link mutation boundary.
package githubstack

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/shhac/gt2gh/internal/diagnostic"
	"github.com/shhac/gt2gh/internal/subprocess"
)

// PullRequest is the GitHub information relevant to a planned stack link.
type PullRequest struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	Head   string `json:"headRefName"`
	Base   string `json:"baseRefName"`
	State  string `json:"state"`
}

// Client invokes gh. Inspect is read-only; Link is only called by --apply.
type Client struct {
	Runner subprocess.Runner
}

// Create creates one pull request without changing any existing PR. It owns
// the temporary body file required by gh, keeping that transport detail out of
// submission planning while preserving Markdown verbatim.
func (c Client) Create(ctx context.Context, branch, base, title, body string, draft bool, reviewers []string) error {
	if c.Runner == nil {
		return fmt.Errorf("GitHub runner is not configured")
	}
	if branch == "" || base == "" || title == "" {
		return fmt.Errorf("pull request branch, base, and title are required")
	}
	bodyFile, err := writeBody(body)
	if err != nil {
		return err
	}
	defer os.Remove(bodyFile)
	args := []string{"pr", "create", "--head", branch, "--base", base, "--title", title, "--body-file", bodyFile}
	if draft {
		args = append(args, "--draft")
	}
	for _, reviewer := range reviewers {
		args = append(args, "--reviewer", reviewer)
	}
	diagnostic.Event(ctx, "github.pr_create", diagnostic.Field{Key: "branch", Value: branch}, diagnostic.Field{Key: "base", Value: base}, diagnostic.Field{Key: "draft", Value: strconv.FormatBool(draft)})
	output, err := c.Runner.Run(ctx, "gh", args...)
	if err != nil {
		return commandError("gh "+strings.Join(args[:6], " ")+" …", err, output)
	}
	return nil
}

func writeBody(body string) (string, error) {
	f, err := os.CreateTemp("", "g2g-submit-body-*.md")
	if err != nil {
		return "", err
	}
	path := f.Name()
	if _, err := f.WriteString(body); err != nil {
		f.Close()
		os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

// CommandError separates a terse external-command summary from its bounded
// diagnostic output so callers can present each once in an appropriate place.
type CommandError struct {
	Command string
	Cause   error
	Output  string
}

func (e *CommandError) Error() string { return e.Command + " failed: " + e.Cause.Error() }
func (e *CommandError) Unwrap() error { return e.Cause }
func (e *CommandError) Summary() string {
	if strings.HasPrefix(e.Command, "gh stack link ") {
		return "gh stack link failed."
	}
	return e.Command + " failed."
}
func (e *CommandError) Diagnostic() string { return boundedMessage(e.Output) }

func (c Client) Inspect(ctx context.Context, branches []string) ([]PullRequest, error) {
	if c.Runner == nil {
		return nil, fmt.Errorf("GitHub runner is not configured")
	}
	repoOutput, err := c.Runner.Run(ctx, "gh", "repo", "view", "--json", "nameWithOwner")
	if err != nil {
		return nil, commandError("gh repo view", err, repoOutput)
	}
	repo, err := parseRepositoryName(repoOutput)
	if err != nil {
		return nil, err
	}
	diagnostic.Event(ctx, "github.repository", diagnostic.Field{Key: "name", Value: repo})
	query := graphqlQuery(repo, branches)
	diagnostic.Event(ctx, "github.query", diagnostic.Field{Key: "kind", Value: "batched_pull_requests"}, diagnostic.Field{Key: "branches", Value: strconv.Itoa(len(branches))}, diagnostic.Field{Key: "query", Value: "omitted"})
	output, err := c.Runner.Run(ctx, "gh", "api", "graphql", "-f", "query="+query)
	if err != nil {
		return nil, commandError("gh api graphql", err, output)
	}
	prs, err := parsePullRequests(output, branches)
	if err != nil {
		return nil, err
	}
	for _, pr := range prs {
		diagnostic.Event(ctx, "github.pull_request",
			diagnostic.Field{Key: "head", Value: pr.Head},
			diagnostic.Field{Key: "base", Value: pr.Base},
			diagnostic.Field{Key: "number", Value: strconv.Itoa(pr.Number)},
			diagnostic.Field{Key: "state", Value: pr.State},
		)
	}
	return prs, nil
}

func parseRepositoryName(output []byte) (string, error) {
	var repo struct {
		NameWithOwner string `json:"nameWithOwner"`
	}
	if err := json.Unmarshal(output, &repo); err != nil || !strings.Contains(repo.NameWithOwner, "/") {
		return "", fmt.Errorf("parse gh repo view JSON")
	}
	return repo.NameWithOwner, nil
}

func parsePullRequests(output []byte, branches []string) ([]PullRequest, error) {
	var response struct {
		Data map[string]struct {
			Nodes []PullRequest `json:"nodes"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		return nil, fmt.Errorf("parse gh api graphql JSON: %w", err)
	}
	if len(response.Errors) != 0 {
		return nil, fmt.Errorf("gh api graphql returned errors: %s", boundedMessage(response.Errors[0].Message))
	}
	matching := make([]PullRequest, 0)
	for index, branch := range branches {
		alias := fmt.Sprintf("pr%d", index)
		result, exists := response.Data[alias]
		if !exists {
			return nil, fmt.Errorf("gh api graphql response is missing %s", alias)
		}
		for _, pr := range result.Nodes {
			if pr.Number <= 0 || pr.Head != branch || pr.Base == "" || pr.State == "" {
				return nil, fmt.Errorf("gh api graphql response has invalid %s pull request", alias)
			}
		}
		matching = append(matching, result.Nodes...)
	}
	sort.Slice(matching, func(left, right int) bool { return matching[left].Head < matching[right].Head })
	return matching, nil
}

func graphqlQuery(repo string, branches []string) string {
	var fields []string
	for index, branch := range branches {
		search := "repo:" + repo + " is:pr head:" + branch
		fields = append(fields, fmt.Sprintf("pr%d: search(query: %s, type: ISSUE, first: 10) { nodes { ... on PullRequest { number url headRefName baseRefName state } } }", index, strconv.Quote(search)))
	}
	return "query { " + strings.Join(fields, " ") + " }"
}

func (c Client) Link(ctx context.Context, trunk string, branches []string) error {
	if c.Runner == nil {
		return fmt.Errorf("GitHub runner is not configured")
	}
	args := append([]string{"stack", "link", "--base", trunk}, branches...)
	diagnostic.Event(ctx, "github.stack_link", diagnostic.Field{Key: "decision", Value: "invoke"}, diagnostic.Field{Key: "base", Value: trunk}, diagnostic.Field{Key: "branches", Value: strings.Join(branches, ",")})
	output, err := c.Runner.Run(ctx, "gh", args...)
	if err != nil {
		return commandError("gh "+strings.Join(args, " "), err, output)
	}
	return nil
}

func commandError(command string, err error, output []byte) error {
	return &CommandError{Command: command, Cause: err, Output: string(output)}
}

func boundedMessage(message string) string {
	const limit = 500
	message = strings.TrimSpace(message)
	if len(message) <= limit {
		return message
	}
	return message[:limit] + "…"
}

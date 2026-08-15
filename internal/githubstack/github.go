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
	Number        int    `json:"number"`
	URL           string `json:"url"`
	Head          string `json:"headRefName"`
	Base          string `json:"baseRefName"`
	State         string `json:"state"`
	StackNumber   int
	StackSize     int
	StackPosition int
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
			diagnostic.Field{Key: "stack_number", Value: strconv.Itoa(pr.StackNumber)},
			diagnostic.Field{Key: "stack_position", Value: strconv.Itoa(pr.StackPosition)},
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
		Data struct {
			Repository map[string]struct {
				Nodes []struct {
					Number int    `json:"number"`
					URL    string `json:"url"`
					Head   string `json:"headRefName"`
					Base   string `json:"baseRefName"`
					State  string `json:"state"`
					Stack  *struct {
						Number int `json:"number"`
						Size   int `json:"size"`
					} `json:"stack"`
					StackEntry *struct {
						Position int `json:"position"`
					} `json:"stackEntry"`
				} `json:"nodes"`
			} `json:"repository"`
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
	if response.Data.Repository == nil {
		return nil, fmt.Errorf("gh api graphql returned no repository; check that the GitHub CLI can read this repository")
	}
	matching := make([]PullRequest, 0)
	for index, branch := range branches {
		alias := fmt.Sprintf("pr%d", index)
		result, exists := response.Data.Repository[alias]
		if !exists {
			return nil, fmt.Errorf("gh api graphql response is missing %s", alias)
		}
		for _, node := range result.Nodes {
			// headRefName filters server-side, so a mismatch is a stray node
			// rather than a malformed response: skip it instead of failing the
			// whole command, including read-only status.
			if node.Head != branch {
				continue
			}
			if node.Number <= 0 || node.Base == "" || node.State == "" {
				return nil, fmt.Errorf("gh api graphql response has invalid %s pull request", alias)
			}
			if (node.Stack == nil) != (node.StackEntry == nil) {
				return nil, fmt.Errorf("gh api graphql response has incomplete native stack data for %s", alias)
			}
			pr := PullRequest{Number: node.Number, URL: node.URL, Head: node.Head, Base: node.Base, State: node.State}
			if node.Stack != nil {
				if node.Stack.Number <= 0 || node.Stack.Size <= 0 || node.StackEntry.Position <= 0 || node.StackEntry.Position > node.Stack.Size {
					return nil, fmt.Errorf("gh api graphql response has invalid native stack data for %s", alias)
				}
				pr.StackNumber = node.Stack.Number
				pr.StackSize = node.Stack.Size
				pr.StackPosition = node.StackEntry.Position
			}
			matching = append(matching, pr)
		}
	}
	sort.Slice(matching, func(left, right int) bool {
		if matching[left].Head != matching[right].Head {
			return matching[left].Head < matching[right].Head
		}
		return matching[left].Number < matching[right].Number
	})
	return matching, nil
}

// graphqlQuery batches one aliased head-ref lookup per selected branch.
//
// headRefName filters the connection server-side, so neither the age of the
// stack nor the repository's pull-request volume affects what comes back. The
// earlier search() form went through GitHub's search index, which lags behind
// newly created pull requests — enough for a branch to look unmapped moments
// after submit created its pull request, and to change between a preview and
// its revalidation — besides matching heads loosely and drawing on the much
// tighter search rate limit.
func graphqlQuery(repo string, branches []string) string {
	owner, name, _ := strings.Cut(repo, "/")
	fields := make([]string, 0, len(branches))
	for index, branch := range branches {
		fields = append(fields, fmt.Sprintf("pr%d: pullRequests(headRefName: %s, first: 10, orderBy: {field: CREATED_AT, direction: DESC}) { nodes { number url headRefName baseRefName state stack { number size } stackEntry { position } } }", index, strconv.Quote(branch)))
	}
	return fmt.Sprintf("query { repository(owner: %s, name: %s) { %s } }", strconv.Quote(owner), strconv.Quote(name), strings.Join(fields, " "))
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

// Unstack removes only the GitHub-native stack relationship identified by its
// GitHub stack number. It does not change branches, PR contents, or Graphite.
func (c Client) Unstack(ctx context.Context, number int) error {
	if c.Runner == nil {
		return fmt.Errorf("GitHub runner is not configured")
	}
	if number <= 0 {
		return fmt.Errorf("GitHub stack number must be positive")
	}
	args := []string{"stack", "unstack", strconv.Itoa(number)}
	diagnostic.Event(ctx, "github.stack_unstack", diagnostic.Field{Key: "stack_number", Value: strconv.Itoa(number)})
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

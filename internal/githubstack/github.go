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
		return nil, repositoryError(err, repoOutput)
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

// repositoryError explains why GitHub could not be reached, rather than
// reporting the exit status of the process that found out.
//
// This is the first thing a reader sees in a repository that has no GitHub
// remote, and "gh repo view failed: exit status 1" tells them nothing they can
// act on. The commands that need a remote say so, and the ones that do not are
// named, because "this tool needs GitHub" would be false.
func repositoryError(err error, output []byte) error {
	detail := strings.TrimSpace(string(output))
	switch {
	case strings.Contains(detail, "no git remotes found"):
		return fmt.Errorf("this repository has no remote, so there are no pull requests to read · g2g graph, track, restack and mirror work without one")
	case strings.Contains(detail, "not a git repository"):
		return fmt.Errorf("this is not a Git repository")
	case strings.Contains(strings.ToLower(detail), "authentication") || strings.Contains(detail, "gh auth login"):
		return fmt.Errorf("GitHub rejected the request · run gh auth login, then rerun")
	}
	return commandError("gh repo view", err, output)
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

// graphqlResponse is the shape of one batched head-ref lookup. Naming it keeps
// parsePullRequests readable and lets node validation be tested directly,
// rather than only through a whole GraphQL envelope.
type graphqlResponse struct {
	Data struct {
		Repository map[string]struct {
			Nodes []pullRequestNode `json:"nodes"`
		} `json:"repository"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type pullRequestNode struct {
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
}

// pullRequest validates one node and converts it. Native stack membership is
// all-or-nothing: a pull request either carries both the stack and its entry
// or neither, and a position must fall inside the stack it claims to be in.
func (n pullRequestNode) pullRequest(alias string) (PullRequest, error) {
	if n.Number <= 0 || n.Base == "" || n.State == "" {
		return PullRequest{}, fmt.Errorf("gh api graphql response has invalid %s pull request", alias)
	}
	if (n.Stack == nil) != (n.StackEntry == nil) {
		return PullRequest{}, fmt.Errorf("gh api graphql response has incomplete native stack data for %s", alias)
	}
	pr := PullRequest{Number: n.Number, URL: n.URL, Head: n.Head, Base: n.Base, State: n.State}
	if n.Stack == nil {
		return pr, nil
	}
	if n.Stack.Number <= 0 || n.Stack.Size <= 0 || n.StackEntry.Position <= 0 || n.StackEntry.Position > n.Stack.Size {
		return PullRequest{}, fmt.Errorf("gh api graphql response has invalid native stack data for %s", alias)
	}
	pr.StackNumber, pr.StackSize, pr.StackPosition = n.Stack.Number, n.Stack.Size, n.StackEntry.Position
	return pr, nil
}

func parsePullRequests(output []byte, branches []string) ([]PullRequest, error) {
	var response graphqlResponse
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
			pr, err := node.pullRequest(alias)
			if err != nil {
				return nil, err
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

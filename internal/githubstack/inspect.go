// Reading the pull requests on a path.
//
// This is the package's one read path, and it is kept apart from the commands
// that change something: what a query returns and how it is parsed changes for
// entirely different reasons than what a mutation sends.
package githubstack

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/shhac/gt2gh/internal/diagnostic"
)

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
		return nil, fmt.Errorf("gh api graphql returned errors: %s", diagnostic.BoundedOutput([]byte(response.Errors[0].Message)))
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

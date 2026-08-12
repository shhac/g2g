// Package githubstack provides read-only PR inspection and the explicit
// GitHub stack-link mutation boundary.
package githubstack

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

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

func (c Client) Inspect(ctx context.Context, branches []string) ([]PullRequest, error) {
	if c.Runner == nil {
		return nil, fmt.Errorf("GitHub runner is not configured")
	}
	repoOutput, err := c.Runner.Run(ctx, "gh", "repo", "view", "--json", "nameWithOwner")
	if err != nil {
		return nil, commandError("gh repo view", err, repoOutput)
	}
	var repo struct {
		NameWithOwner string `json:"nameWithOwner"`
	}
	if err := json.Unmarshal(repoOutput, &repo); err != nil || !strings.Contains(repo.NameWithOwner, "/") {
		return nil, fmt.Errorf("parse gh repo view JSON")
	}
	query := graphqlQuery(repo.NameWithOwner, branches)
	output, err := c.Runner.Run(ctx, "gh", "api", "graphql", "-f", "query="+query)
	if err != nil {
		return nil, commandError("gh api graphql", err, output)
	}
	var response struct {
		Data map[string]struct {
			Nodes []PullRequest `json:"nodes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		return nil, fmt.Errorf("parse gh api graphql JSON: %w", err)
	}
	var matching []PullRequest
	for index := range branches {
		matching = append(matching, response.Data[fmt.Sprintf("pr%d", index)].Nodes...)
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
	output, err := c.Runner.Run(ctx, "gh", args...)
	if err != nil {
		return commandError("gh "+strings.Join(args, " "), err, output)
	}
	return nil
}

func commandError(command string, err error, output []byte) error {
	message := strings.TrimSpace(string(output))
	if message == "" {
		return fmt.Errorf("%s failed: %w", command, err)
	}
	return fmt.Errorf("%s failed: %w (%s)", command, err, message)
}

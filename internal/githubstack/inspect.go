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

	"github.com/shhac/g2g/internal/diagnostic"
)

func (c Client) Inspect(ctx context.Context, branches []string) ([]PullRequest, error) {
	if c.Runner == nil {
		return nil, fmt.Errorf("GitHub runner is not configured")
	}
	// One round trip, not two. Which repository this is was asked for
	// separately, and it is the only thing that answer was used for: gh fills
	// {owner} and {repo} from the repository of the current directory, which is
	// the same resolution gh repo view performed — documented for --field, and
	// the query names the repository back so a reader can still see which one
	// answered.
	query := graphqlQuery(branches)
	diagnostic.Event(ctx, "github.query", diagnostic.Field{Key: "kind", Value: "batched_pull_requests"}, diagnostic.Field{Key: "branches", Value: strconv.Itoa(len(branches))}, diagnostic.Field{Key: "query", Value: "omitted"})
	output, err := c.Runner.Run(ctx, "gh", "api", "graphql", "-F", "owner={owner}", "-F", "name={repo}", "-f", "query="+query)
	if err != nil {
		return nil, repositoryError(err, output)
	}
	if repo := repositoryName(output); repo != "" {
		diagnostic.Event(ctx, "github.repository", diagnostic.Field{Key: "name", Value: repo})
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
	return commandError("gh api graphql", err, output)
}

// repositoryName reads back which repository answered, for the diagnostic. It
// is absent from a response that failed, and saying nothing is better there
// than reporting a repository nobody read from.
func repositoryName(output []byte) string {
	var response graphqlResponse
	if err := json.Unmarshal(output, &response); err != nil {
		return ""
	}
	var name string
	if err := json.Unmarshal(response.Data.Repository["nameWithOwner"], &name); err != nil {
		return ""
	}
	return name
}

// graphqlResponse is the shape of one batched head-ref lookup. Naming it keeps
// parsePullRequests readable and lets node validation be tested directly,
// rather than only through a whole GraphQL envelope.
type graphqlResponse struct {
	Data struct {
		// Repository is keyed by field alias, and the fields are not all the
		// same shape: one names the repository and the rest are pull request
		// connections. Decoding each where it is read keeps one query able to
		// answer both.
		Repository map[string]json.RawMessage `json:"repository"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type pullRequestNode struct {
	Number  int    `json:"number"`
	URL     string `json:"url"`
	Head    string `json:"headRefName"`
	HeadOID string `json:"headRefOid"`
	Base    string `json:"baseRefName"`
	State   string `json:"state"`
	Stack   *struct {
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
	pr := PullRequest{Number: n.Number, URL: n.URL, Head: n.Head, HeadOID: n.HeadOID, Base: n.Base, State: n.State}
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
		raw, exists := response.Data.Repository[alias]
		if !exists {
			return nil, fmt.Errorf("gh api graphql response is missing %s", alias)
		}
		var result struct {
			Nodes []pullRequestNode `json:"nodes"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, fmt.Errorf("gh api graphql response has invalid %s pull requests", alias)
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
func graphqlQuery(branches []string) string {
	fields := make([]string, 0, len(branches)+1)
	// Named back so the diagnostic can still say which repository answered,
	// now that nothing here resolved it.
	fields = append(fields, "nameWithOwner")
	for index, branch := range branches {
		fields = append(fields, fmt.Sprintf("pr%d: pullRequests(headRefName: %s, first: 10, orderBy: {field: CREATED_AT, direction: DESC}) { nodes { number url headRefName headRefOid baseRefName state stack { number size } stackEntry { position } } }", index, strconv.Quote(branch)))
	}
	return fmt.Sprintf("query($owner: String!, $name: String!) { repository(owner: $owner, name: $name) { %s } }", strings.Join(fields, " "))
}

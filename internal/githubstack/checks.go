package githubstack

import (
	"context"
	"fmt"
	"strings"
)

// Checks describes only required contexts; an incomplete answer stays unknown.
type Checks struct {
	Complete                   bool
	Failed, Cancelled, Pending []string
}

type checkNode struct {
	Name       string `json:"name"`
	Context    string `json:"context"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
	Required   bool   `json:"isRequired"`
	URL        string `json:"detailsUrl"`
	TargetURL  string `json:"targetUrl"`
}

func (c Client) requiredChecks(ctx context.Context, numbers []int) (map[int]Checks, error) {
	fields := make([]string, 0, len(numbers))
	for i, n := range numbers {
		fields = append(fields, fmt.Sprintf(`c%d: pullRequest(number: %d) { statusCheckRollup { contexts(first: 100) { pageInfo { hasNextPage } nodes { ... on CheckRun { name status conclusion detailsUrl isRequired(pullRequestNumber: %d) } ... on StatusContext { context state targetUrl isRequired(pullRequestNumber: %d) } } } } }`, i, n, n, n))
	}
	query := `query RequiredChecks($owner: String!, $name: String!) { repository(owner: $owner, name: $name) { ` + strings.Join(fields, " ") + ` } }`
	output, err := c.graphql(ctx, "required_checks", "pull_requests", len(numbers), query)
	if err != nil {
		return nil, repositoryError(err, output)
	}
	repository, err := repositoryFields(output, nil)
	if err != nil {
		return nil, err
	}
	result := make(map[int]Checks, len(numbers))
	for i, n := range numbers {
		var node struct {
			Rollup *struct {
				Contexts struct {
					PageInfo struct {
						More bool `json:"hasNextPage"`
					} `json:"pageInfo"`
					Nodes []checkNode `json:"nodes"`
				} `json:"contexts"`
			} `json:"statusCheckRollup"`
		}
		if err := aliasField(repository, fmt.Sprintf("c%d", i), "checks", &node); err != nil {
			return nil, err
		}
		if node.Rollup == nil {
			result[n] = Checks{}
			continue
		}
		result[n] = classifyChecks(node.Rollup.Contexts.Nodes, node.Rollup.Contexts.PageInfo.More)
	}
	return result, nil
}

// classifyChecks retains required results only. Cancellation precedes failure,
// and failure precedes pending status; an unknown completed result says nothing.
func classifyChecks(nodes []checkNode, more bool) Checks {
	detail := Checks{Complete: !more}
	for _, check := range nodes {
		if !check.Required {
			continue
		}
		name := check.Name
		if name == "" {
			name = check.Context
		}
		switch {
		case check.Conclusion == "CANCELLED":
			detail.Cancelled = append(detail.Cancelled, name)
		case failedCheck(check):
			detail.Failed = append(detail.Failed, name)
		case check.State == "PENDING" || (check.Status != "" && check.Status != "COMPLETED"):
			detail.Pending = append(detail.Pending, name)
		}
	}
	return detail
}

func failedCheck(check checkNode) bool {
	if check.State == "FAILURE" || check.State == "ERROR" {
		return true
	}
	switch check.Conclusion {
	case "FAILURE", "TIMED_OUT", "ACTION_REQUIRED", "STARTUP_FAILURE":
		return true
	}
	return false
}

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
		detail := Checks{}
		if node.Rollup != nil {
			detail.Complete = !node.Rollup.Contexts.PageInfo.More
			for _, check := range node.Rollup.Contexts.Nodes {
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
				case check.State == "FAILURE" || check.State == "ERROR" || check.Conclusion == "FAILURE" || check.Conclusion == "TIMED_OUT" || check.Conclusion == "ACTION_REQUIRED" || check.Conclusion == "STARTUP_FAILURE":
					detail.Failed = append(detail.Failed, name)
				case check.State == "PENDING" || (check.Status != "" && check.Status != "COMPLETED"):
					detail.Pending = append(detail.Pending, name)
				}
			}
		}
		result[n] = detail
	}
	return result, nil
}

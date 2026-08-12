// Package githubstack provides read-only PR inspection and the explicit
// GitHub stack-link mutation boundary.
package githubstack

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
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
	output, err := c.Runner.Run(ctx, "gh", "pr", "list", "--state", "all", "--limit", "1000", "--json", "number,url,headRefName,baseRefName,state")
	if err != nil {
		return nil, commandError("gh pr list", err, output)
	}
	var all []PullRequest
	if err := json.Unmarshal(output, &all); err != nil {
		return nil, fmt.Errorf("parse gh pr list JSON: %w", err)
	}
	wanted := make(map[string]bool, len(branches))
	for _, branch := range branches {
		wanted[branch] = true
	}
	var matching []PullRequest
	for _, pr := range all {
		if wanted[pr.Head] {
			matching = append(matching, pr)
		}
	}
	sort.Slice(matching, func(left, right int) bool { return matching[left].Head < matching[right].Head })
	return matching, nil
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

package githubstack

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
)

// createdPullRequest reads gh pr create's URL without another network call.
// Combined output can also contain warnings; only a URL naming a PR counts.
func createdPullRequest(output []byte, branch, base string) (PullRequest, bool) {
	for _, line := range strings.Split(string(output), "\n") {
		address := strings.TrimSpace(line)
		u, err := url.Parse(address)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			continue
		}
		prefix, number, ok := strings.Cut(u.Path, "/pull/")
		n, err := strconv.Atoi(number)
		if !ok || prefix == "" || err != nil || n <= 0 {
			continue
		}
		return PullRequest{Number: n, URL: address, Head: branch, Base: base, State: "OPEN"}, true
	}
	return PullRequest{}, false
}

func (c Client) remember(ctx context.Context, prs []PullRequest) {
	if c.Observations == nil {
		return
	}
	c.observationError(ctx, c.Observations.Remember(ctx, prs))
}

func (c Client) observationError(ctx context.Context, err error) {
	if err != nil {
		diagnostic.Warn(ctx, "github.observations", "could not remember PR state locally: "+err.Error())
	}
}

package githubstack

import (
	"context"
	"fmt"
)

// RequestQueue lets GitHub enqueue once requirements pass. ExpectedHeadOid
// pins the reviewed head; the required queue chooses its own merge method.
func (c Client) RequestQueue(ctx context.Context, number int, id, head string, autoMerge ...bool) error {
	if number <= 0 || id == "" || head == "" {
		return fmt.Errorf("queue request needs pull request identity and expected head")
	}
	operation, name := "RequestMergeQueue", "enablePullRequestAutoMerge"
	if len(autoMerge) != 0 && !autoMerge[0] {
		operation, name = "EnqueueMergeQueue", "enqueuePullRequest"
	}
	outcomes, err := c.mutate(ctx, operation, []mutationField{{name: name, inputs: []mutationInput{
		{field: "pullRequestId", variable: "pullRequest", kind: "ID!", value: id},
		{field: "expectedHeadOid", variable: "head", kind: "GitObjectID!", value: head},
	}}})
	if err != nil {
		return err
	}
	if outcomes[0] != nil {
		return outcomes[0]
	}
	if c.Observations != nil {
		c.observationError(ctx, c.Observations.MergeRequested(ctx, number, head))
	}
	return nil
}

package link

import (
	"context"

	"github.com/shhac/g2g/internal/parallel"
)

// eachBranch runs read over every branch, several at a time, and returns the
// first error any of them gave.
//
// The bounding and the cancellation live in internal/parallel, because
// internal/graph needs the same thing and two copies of bounded concurrency
// drift. This keeps the name the call sites read best.
func eachBranch(ctx context.Context, branches []string, read func(context.Context, int, string) error) error {
	return parallel.Each(ctx, branches, read)
}

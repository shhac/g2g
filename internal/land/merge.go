package land

import (
	"context"
	"fmt"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/githubstack"
)

// mergeReady retries only the explicit base-change refusal, at most twice.
// Each retry waits for recomputation and rechecks approval, protection, head
// and base. The merge itself is pinned to the head this descent published.
func (s Service) mergeReady(ctx context.Context, plan Plan, step Step, tip string) error {
	wait := s.pause
	if wait == nil {
		wait = pause
	}
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		now, err := s.recheck(ctx, plan, step, tip)
		if err != nil {
			return err
		}
		diagnostic.Event(ctx, "land.merge",
			diagnostic.Field{Key: "branch", Value: step.Branch},
			diagnostic.Field{Key: "number", Value: fmt.Sprint(step.Number)},
			diagnostic.Field{Key: "admin", Value: fmt.Sprintf("%t", now.Admin || step.Admin)},
			diagnostic.Field{Key: "attempt", Value: fmt.Sprint(attempt)},
		)
		err = s.GitHub.Merge(ctx, step.Number, plan.Options.Method, now.Admin || step.Admin, tip)
		if !githubstack.BaseModified(err) {
			return err
		}
		if attempt == 3 {
			return fmt.Errorf("the base of #%d kept changing after three merge attempts · rerun g2g land: %w", step.Number, err)
		}
		if err := wait(ctx, settleFirst); err != nil {
			return err
		}
		if err := s.settlePush(ctx, step, tip, plan.Options.Remote); err != nil {
			return err
		}
	}
}

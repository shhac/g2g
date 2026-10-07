package land

import (
	"context"
	"fmt"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/githubstack"
)

// Stopped reports a descent that stopped part-way.
//
// Landing is a sequence, so "it happened" and "it did not" are not the only
// two answers, and the branches that did land stay landed. It carries what
// finished rather than leaving the caller to go and find out, because the
// caller asks while the command is failing and the budget it would ask under
// has usually expired with it.
type Stopped struct {
	// Landed names the branches whose merge completed, in the order they did.
	// A merge completes when GitHub accepts it, so the branch the descent
	// stopped at is among them when what failed came after its merge.
	Landed []string
	// Tidied names the branches that had already landed and whose cleanup this
	// run finished: forgotten and deleted, which is not coming back either.
	Tidied []string
	// Changed says what else this run did that stays done: a branch published,
	// a pull request retargeted, the stack brought up to date after a merge.
	// None of it is a merge, and all of it is on the remote or in the graph.
	Changed []string
	// Branch is where it stopped.
	Branch string
	Err    error
}

// PartWay reports a descent that changed something before it stopped.
//
// One that changed nothing is a failure like any other, and saying it stopped
// part-way -- with the exit status that says so -- told a script something had
// landed when nothing had.
func (e *Stopped) PartWay() bool {
	return len(e.Landed) != 0 || len(e.Tidied) != 0 || len(e.Changed) != 0
}

func (e *Stopped) Error() string {
	if len(e.Landed) == 0 {
		return fmt.Sprintf("stopped at %s: %v", e.Branch, e.Err)
	}
	return fmt.Sprintf("landed %s, then stopped at %s: %v", strings.Join(e.Landed, ", "), e.Branch, e.Err)
}

func (e *Stopped) Unwrap() error { return e.Err }

// Apply takes the stack down, one branch at a time, bottom first.
//
// It stops at the first step that cannot finish rather than unwinding. A merge
// cannot be taken back, and the branches below the failure are exactly where
// they should be.
func (s Service) Apply(ctx context.Context, plan Plan) error {
	if plan.Blocked() != "" {
		return fmt.Errorf("cannot land: %s", plan.Blocked())
	}
	if err := plan.RequireActionable("g2g land"); err != nil {
		return err
	}
	landed := make([]string, 0, len(plan.Steps))
	tidied := make([]string, 0, len(plan.Steps))
	changed := make([]string, 0)
	for _, step := range plan.Steps {
		merged, err := s.cycle(ctx, plan, step, &changed)
		// Recorded before the error is looked at. A merge is done once GitHub
		// accepts it, whatever fails after -- the wait for it to reach the base,
		// the replay above it -- and reporting only whole cycles told someone
		// whose branch had merged that it had not.
		if merged {
			landed = append(landed, step.Branch)
		}
		if err != nil {
			return &Stopped{Landed: landed, Tidied: tidied, Changed: changed, Branch: step.Branch, Err: err}
		}
		if !step.Merges() {
			tidied = append(tidied, step.Branch)
		}
	}
	for _, above := range plan.Republish {
		pushed, err := s.republish(ctx, plan, above)
		if pushed {
			changed = append(changed, "published "+above.Branch)
		}
		if err != nil {
			return &Stopped{Landed: landed, Tidied: tidied, Changed: changed, Branch: above.Branch, Err: err}
		}
	}
	return nil
}

// cycle lands one branch and tidies up after it, and says whether the merge
// happened even when something after it did not.
//
// changed collects what it does short of a merge that stays done, so a stop
// before the merge still says the remote moved.
func (s Service) cycle(ctx context.Context, plan Plan, step Step, changed *[]string) (merged bool, err error) {
	diagnostic.Event(ctx, "land.cycle",
		diagnostic.Field{Key: "branch", Value: step.Branch},
		diagnostic.Field{Key: "merges", Value: fmt.Sprintf("%t", step.Merges())},
	)
	if step.Merges() {
		if merged, err = s.merge(ctx, plan, step, changed); err != nil {
			return merged, err
		}
	}
	return merged, s.tidy(ctx, plan, step, changed)
}

// merge publishes the branch, points its pull request at the right base, waits
// for GitHub to have seen both, and merges. It reports the merge as done once
// GitHub has accepted it, even if the wait after it then fails.
func (s Service) merge(ctx context.Context, plan Plan, step Step, changed *[]string) (bool, error) {
	// Unconditionally, whatever the plan said. Step.Push was decided before
	// anything moved, and by the time a branch above the first comes round its
	// replay has rewritten it -- so a plan that said "already published" is
	// describing a commit that no longer exists. push answers the question
	// again from the tips it reads itself and does nothing when there is
	// nothing to do, which makes asking it every time free and trusting the
	// preview wrong.
	pushed, err := s.publish(ctx, plan, step)
	if err != nil {
		return false, err
	}
	if pushed {
		*changed = append(*changed, "published "+step.Branch)
	}
	if step.Retargets() {
		// The base only goes stale during the run: the branch below merged and
		// GitHub has not yet moved this pull request off it. Merging it there
		// would put the work into a branch that is about to be deleted, and
		// report success.
		diagnostic.Event(ctx, "land.retarget",
			diagnostic.Field{Key: "number", Value: fmt.Sprint(step.Number)},
			diagnostic.Field{Key: "from", Value: step.From},
			diagnostic.Field{Key: "to", Value: step.Base},
		)
		if err := s.GitHub.Retarget(ctx, step.Number, step.Base); err != nil {
			return false, err
		}
		*changed = append(*changed, fmt.Sprintf("pointed #%d at %s", step.Number, step.Base))
	}
	tip, err := s.Git.Resolve(ctx, step.Branch)
	if err != nil {
		return false, err
	}
	if err := s.settlePush(ctx, step, tip, plan.Options.Remote); err != nil {
		return false, err
	}
	// Decided again, immediately before the one act that cannot be undone. The
	// plan was made before anything moved; this branch has since been rebuilt
	// and republished, and its pull request has been pointed somewhere else.
	//
	// And the merge is made on what was decided now. Whether it needs --admin
	// is the part most likely to have changed: the replay's force-push is what
	// restarts the required checks, so a branch that was clean when planned is
	// blocked by the time its turn comes -- and merging it on the plan's answer
	// asked GitHub for a merge it refuses, having been given --admin to pass.
	// The plan's forecast still counts, so the merge is never less than the
	// recipe said it would be.
	if err := s.mergeReady(ctx, plan, step, tip); err != nil {
		return false, err
	}
	return true, s.settleMerge(ctx, plan, step)
}

// recheck decides this branch again against the world as it is now, and
// answers with that decision.
func (s Service) recheck(ctx context.Context, plan Plan, step Step, expectedTip string) (Step, error) {
	prs, err := s.GitHub.Inspect(ctx, []string{step.Branch})
	if err != nil {
		return Step{}, err
	}
	state, err := s.state(ctx, step.Number)
	if err != nil {
		return Step{}, err
	}
	tip, err := s.Git.Resolve(ctx, step.Branch)
	if err != nil {
		return Step{}, err
	}
	if tip != expectedTip {
		return Step{}, fmt.Errorf("%s changed locally before merging · rerun g2g land", step.Branch)
	}
	decided := step
	for path := range githubstack.Along(step.Base, []string{step.Branch}, prs) {
		now, note := classify(facts{Step: path, State: state, Current: true, Tip: tip, Admin: plan.Options.Admin})
		if note.Reason != "" {
			return Step{}, fmt.Errorf("%s", note.Sentence())
		}
		if now.Number != step.Number || now.Retargets() || state.HeadOID != tip || state.Base != step.Base {
			return Step{}, fmt.Errorf("#%d changed its head or base before merging · rerun g2g land", step.Number)
		}
		decided = now
	}
	return decided, nil
}

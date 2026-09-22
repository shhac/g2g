package land

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
	syncer "github.com/shhac/g2g/internal/sync"
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
	// Branch is where it stopped.
	Branch string
	Err    error
}

// PartWay reports a descent that changed something before it stopped.
//
// One that changed nothing is a failure like any other, and saying it stopped
// part-way -- with the exit status that says so -- told a script something had
// landed when nothing had.
func (e *Stopped) PartWay() bool { return len(e.Landed) != 0 || len(e.Tidied) != 0 }

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
	if plan.Blocked != "" {
		return fmt.Errorf("cannot land: %s", plan.Blocked)
	}
	if err := plan.RequireActionable("g2g land"); err != nil {
		return err
	}
	landed := make([]string, 0, len(plan.Steps))
	tidied := make([]string, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		merged, err := s.cycle(ctx, plan, step)
		// Recorded before the error is looked at. A merge is done once GitHub
		// accepts it, whatever fails after -- the wait for it to reach the base,
		// the replay above it -- and reporting only whole cycles told someone
		// whose branch had merged that it had not.
		if merged {
			landed = append(landed, step.Branch)
		}
		if err != nil {
			return &Stopped{Landed: landed, Tidied: tidied, Branch: step.Branch, Err: err}
		}
		if !step.Merges() {
			tidied = append(tidied, step.Branch)
		}
	}
	return nil
}

// cycle lands one branch and tidies up after it, and says whether the merge
// happened even when something after it did not.
func (s Service) cycle(ctx context.Context, plan Plan, step Step) (merged bool, err error) {
	diagnostic.Event(ctx, "land.cycle",
		diagnostic.Field{Key: "branch", Value: step.Branch},
		diagnostic.Field{Key: "merges", Value: fmt.Sprintf("%t", step.Merges())},
	)
	if step.Merges() {
		if merged, err = s.merge(ctx, plan, step); err != nil {
			return merged, err
		}
	}
	return merged, s.tidy(ctx, plan, step)
}

// merge publishes the branch, points its pull request at the right base, waits
// for GitHub to have seen both, and merges. It reports the merge as done once
// GitHub has accepted it, even if the wait after it then fails.
func (s Service) merge(ctx context.Context, plan Plan, step Step) (bool, error) {
	// Unconditionally, whatever the plan said. Step.Push was decided before
	// anything moved, and by the time a branch above the first comes round its
	// replay has rewritten it -- so a plan that said "already published" is
	// describing a commit that no longer exists. push answers the question
	// again from the tips it reads itself and does nothing when there is
	// nothing to do, which makes asking it every time free and trusting the
	// preview wrong.
	if err := s.publish(ctx, plan, step); err != nil {
		return false, err
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
	now, err := s.recheck(ctx, plan, step)
	if err != nil {
		return false, err
	}
	diagnostic.Event(ctx, "land.merge",
		diagnostic.Field{Key: "branch", Value: step.Branch},
		diagnostic.Field{Key: "number", Value: fmt.Sprint(step.Number)},
		diagnostic.Field{Key: "admin", Value: fmt.Sprintf("%t", now.Admin || step.Admin)},
	)
	if err := s.GitHub.Merge(ctx, step.Number, plan.Options.Method, now.Admin || step.Admin); err != nil {
		return false, err
	}
	return true, s.settleMerge(ctx, plan, step)
}

// publish pushes exactly this branch, through push's own plan.
//
// Through the service rather than straight to git: push refuses a branch the
// remote has moved on, and a lease built here from tips read here would always
// match its own reading and overwrite whatever a reviewer had pushed -- and
// then merge it.
func (s Service) publish(ctx context.Context, plan Plan, step Step) error {
	// Whether this branch's published version is its own or somebody else's is
	// the one question push cannot answer here. After a replay the remote
	// holds commits the branch no longer has, which is exactly the shape of a
	// reviewer's fix, and push refuses both. What tells them apart is whether
	// the remote still holds what the plan saw: land moves these refs itself
	// and knows what it left there.
	tips, err := s.Git.RemoteTips(ctx, plan.Options.Remote, []string{step.Branch})
	if err != nil {
		return err
	}
	if tips[step.Branch] != step.RemoteTip {
		return fmt.Errorf("%s has moved on %s since this was planned, so it carries work this descent has not seen · fetch and reconcile it, then rerun", plan.Options.Remote, step.Branch)
	}
	// The path, not the branch alone: push needs a base to compare against and
	// a single-branch selection has no ancestry to take one from. Landing
	// forgets each branch as it lands, so by the time this runs the path from
	// the trunk holds exactly the branch being published.
	published, err := s.Pusher.Plan(ctx, stack.Selection{Branch: step.Branch, Trunk: plan.Trunk, Scope: shape.ScopePath}, plan.Options.Remote)
	if err != nil {
		return err
	}
	// More than one branch here means an earlier cycle did not tidy up, and the
	// extra one has already merged -- pushing it would put it back.
	if len(published.Branches) != 1 || published.Branches[0] != step.Branch {
		return fmt.Errorf("publishing %s would also push %s, which has already landed", step.Branch, strings.Join(published.Branches, ", "))
	}
	if published.NothingToPublish() {
		return nil
	}
	// push's own refusal is deliberately not consulted: it is the "the remote
	// has moved" check, which the comparison above has just answered more
	// precisely. Its lease still guards the push itself, pinned to the tips it
	// read, so a ref that moves between here and the push is still rejected.
	return s.Pusher.Execute(ctx, published)
}

// settlePush waits until GitHub is looking at what was just pushed.
//
// Three facts, not one. headRefOid says GitHub has the commit; baseRefName
// says the merge will go where it is meant to; mergeable says GitHub has
// finished working out whether it can merge at all, which it reports as
// UNKNOWN while it thinks. None of them is about CI.
func (s Service) settlePush(ctx context.Context, step Step, tip string, remote string) error {
	err := settle(ctx, s.pause, fmt.Sprintf("GitHub to see %s at %s", step.Branch, shortID(tip)), func(ctx context.Context) (bool, error) {
		state, err := s.state(ctx, step.Number)
		if err != nil {
			return false, err
		}
		return state.HeadOID == tip && state.Base == step.Base && state.Mergeable != githubstack.MergeableUnknown, nil
	})
	var unsettled *NotSettled
	if !errors.As(err, &unsettled) {
		return err
	}
	// Which of the two failed. "Waiting for GitHub" reads as propagation lag,
	// and the answer to that is to wait longer -- so a push that never reached
	// the remote at all sends somebody to raise a timeout that was never the
	// problem. The remote is one cheap read and it separates them.
	//
	// Asked on a context of its own, because the wait only gives up once the
	// budget has expired, and a read on that context never runs: the diagnosis
	// existed and could not happen, which only a fake that ignored the context
	// failed to notice. Bounded, so an unreachable remote cannot hold a command
	// that has already run out of time.
	diagnose, cancel := context.WithTimeout(context.WithoutCancel(ctx), diagnoseBudget)
	defer cancel()
	tips, readErr := s.Git.RemoteTips(diagnose, remote, []string{step.Branch})
	if readErr != nil {
		return err
	}
	if published, present := tips[step.Branch]; !present || published != tip {
		return fmt.Errorf("%s is not on %s at %s (it has %s), so GitHub was never going to see it · the push did not take effect",
			step.Branch, remote, shortID(tip), describeTip(published))
	}
	return err
}

// describeTip names a tip a remote does not have, so the sentence above reads
// as a fact either way.
func describeTip(tip string) string {
	if tip == "" {
		return "no such branch"
	}
	return shortID(tip)
}

// settleMerge waits until the merge has reached the base on the remote.
//
// By ancestry, never by watching the tip change: a colleague's unrelated push
// changes it too, and acting on that fetches a base that does not contain this
// merge and replays the branch above onto it. The merge commit is already in
// the answer GitHub gives about the merge it just performed.
func (s Service) settleMerge(ctx context.Context, plan Plan, step Step) error {
	return settle(ctx, s.pause, fmt.Sprintf("the merge of %s to reach %s", step.Branch, plan.Trunk), func(ctx context.Context) (bool, error) {
		state, err := s.state(ctx, step.Number)
		if err != nil {
			return false, err
		}
		if !state.Merged() || state.MergeCommit == "" {
			return false, nil
		}
		if err := s.Git.FetchIsolated(ctx, plan.Options.Remote, []string{plan.Trunk}); err != nil {
			return false, err
		}
		return s.Git.IsAncestor(ctx, state.MergeCommit, localgit.IsolatedRef(plan.Options.Remote, plan.Trunk))
	})
}

func (s Service) state(ctx context.Context, number int) (githubstack.MergeState, error) {
	mergeability, err := s.GitHub.Mergeability(ctx, []int{number})
	if err != nil {
		return githubstack.MergeState{}, err
	}
	return mergeability.States[number], nil
}

// recheck decides this branch again against the world as it is now, and
// answers with that decision.
func (s Service) recheck(ctx context.Context, plan Plan, step Step) (Step, error) {
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
	decided := step
	for path := range githubstack.Along(step.Base, []string{step.Branch}, prs) {
		now, note := classify(facts{Step: path, State: state, Current: true, Tip: tip, Admin: plan.Options.Admin})
		if note.Reason != "" {
			return Step{}, fmt.Errorf("%s", note.Sentence())
		}
		decided = now
	}
	return decided, nil
}

// tidy brings the rest of the stack onto the advanced trunk and forgets the
// branch that has just landed.
//
// Nothing here can stop the descent. The work is merged; a branch that could
// not be deleted is untidy, and reporting it as a failed land would be wrong
// about the thing that matters and would strand the branches above.
func (s Service) tidy(ctx context.Context, plan Plan, step Step) error {
	if err := s.advance(ctx, plan); err != nil {
		return err
	}
	if plan.Options.Forget {
		if err := s.forget(ctx, step.Branch); err != nil {
			return err
		}
	}
	s.remove(ctx, plan, step)
	return nil
}

// advance fast-forwards the base and replays what is left onto it.
func (s Service) advance(ctx context.Context, plan Plan) error {
	// The top of what remains, always. A guard here read as a special case for
	// the last step and was not one: where it held, index was already the last
	// index, so both branches named the same branch.
	target := plan.Steps[len(plan.Steps)-1].Branch
	synced, err := s.Syncer.Plan(ctx, graph.Selection{Branch: target, Scope: graph.ScopeStack}, plan.Options.Remote, syncer.TakeNothing)
	if err != nil {
		return err
	}
	if synced.Blocked != "" {
		return fmt.Errorf("cannot bring the rest of the stack up to date: %s", synced.Blocked)
	}
	if synced.Nothing() {
		return nil
	}
	return s.Syncer.Apply(ctx, synced)
}

// forget reparents what sat on the landed branch, then drops it from the graph.
//
// In that order. Forgetting first strands the children, which is what prune
// refuses to do; reparenting first leaves the landed branch with none, so the
// refusal never applies. The children keep their own fork points, which is
// what keeps their replay ranges to their own commits.
func (s Service) forget(ctx context.Context, landed string) error {
	adopted, err := s.Graph.Store.Load(ctx)
	if err != nil {
		return err
	}
	edge, tracked := adopted.Edges[landed]
	if !tracked {
		return nil
	}
	updated := adopted
	for _, child := range adopted.Children(landed) {
		inherited := updated.Edges[child]
		inherited.Parent = edge.Parent
		if updated, _, err = updated.Adopt(child, inherited); err != nil {
			return err
		}
	}
	if err := s.Graph.Store.Save(ctx, updated); err != nil {
		return err
	}
	pruned, err := s.Pruner.Plan(ctx, graph.Selection{Branch: landed, Scope: graph.ScopeBranch})
	if err != nil {
		return err
	}
	if pruned.Blocked != "" || pruned.Nothing() {
		return nil
	}
	return s.Pruner.Apply(ctx, pruned)
}

// remove deletes the branch's refs, here and on the remote.
//
// Every failure is swallowed and reported as a diagnostic rather than
// returned. The pull request is merged: a ref that would not delete is
// untidiness, and turning it into a failed land would both misreport what
// happened and stop the branches above from landing at all.
func (s Service) remove(ctx context.Context, plan Plan, step Step) {
	if plan.Options.DeleteRemote {
		if err := s.Git.DeleteRemoteBranch(ctx, plan.Options.Remote, step.Branch); err != nil {
			diagnostic.Warn(ctx, "land.delete_remote", fmt.Sprintf("could not delete %s on %s: %v", step.Branch, plan.Options.Remote, err))
		}
	}
	if !plan.Options.DeleteLocal {
		return
	}
	// git will not delete the branch that is checked out, and landing a whole
	// stack from its top ends standing on the last one deleted.
	if current, err := s.Git.CurrentBranch(ctx); err == nil && current == step.Branch {
		if err := s.Git.SwitchBranch(ctx, plan.Trunk); err != nil {
			diagnostic.Warn(ctx, "land.switch", fmt.Sprintf("could not move off %s to %s: %v", step.Branch, plan.Trunk, err))
			return
		}
	}
	if err := s.Git.DeleteBranch(ctx, step.Branch); err != nil {
		diagnostic.Warn(ctx, "land.delete_local", fmt.Sprintf("could not delete %s: %v", step.Branch, err))
	}
}

func shortID(object string) string {
	if len(object) <= 7 {
		return object
	}
	return object[:7]
}

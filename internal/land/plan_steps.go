package land

import (
	"cmp"
	"context"
	"slices"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/stack"
)

// decideSteps decides every branch of the descent in order, or the reason the
// whole descent is refused.
//
// checked are the remote tips push's preflight found hold nothing of the
// remote's own, which is what a branch already landed may be deleted at.
func (s Service) decideSteps(ctx context.Context, discovery stack.Discovery, options Options, mergeability githubstack.Mergeability, checked map[string]string) ([]Step, repair.Note, error) {
	tips, err := s.Git.RemoteTips(ctx, options.Remote, discovery.Branches)
	if err != nil {
		return nil, repair.Note{}, err
	}
	trunks, err := s.trunksToAsk(ctx, discovery, discovery.Base, options.Remote)
	if err != nil {
		return nil, repair.Note{}, err
	}
	steps := make([]Step, 0, len(discovery.Branches))
	for step := range githubstack.Across(intoTrunk(discovery.Base, discovery.Branches), discovery.Branches, discovery.PullRequests) {
		tip, _ := s.Git.Resolve(ctx, step.Branch)
		decided, note := classify(facts{
			Step:           step,
			State:          stateFor(mergeability, step),
			Landed:         s.landedIn(ctx, step.Branch, trunks),
			Current:        tip != "" && tips[step.Branch] == tip,
			Tip:            tip,
			Queue:          mergeability.Queue != nil,
			QueueAutoMerge: mergeability.Queue != nil && mergeability.Queue.AutoMergeAllowed,
			Admin:          options.Admin,
		})
		if note.Reason != "" {
			return nil, note, nil
		}
		decided.HeadOID = tip
		decided.RemoteTip = tips[step.Branch]
		if decided.Landed {
			decided.LandedHead = deletableHead(tip, checked[step.Branch])
		}
		decided.Queue = mergeability.Queue != nil
		if decided.Merges() && decided.Push && decided.RemoteTip != "" {
			below := cmp.Or(discovery.Parents[step.Branch], discovery.Base)
			if decided.Unpublished, err = s.unpublished(ctx, decided.RemoteTip, tip, below); err != nil {
				return nil, repair.Note{}, err
			}
		}
		// Everything above the bottom branch is replayed onto the advanced
		// trunk before its turn, which rewrites it, so it will need publishing
		// however current it looks now.
		decided.Push = decided.Push || len(steps) != 0
		steps = append(steps, decided)
	}
	return steps, repair.Note{}, nil
}

// forecastAdmin says which merges will need --admin once their own restack
// has restarted the checks. Without --admin that is a warning the preview
// carries (Protected); with it, the mark goes on each step where it will be
// needed, so the preview and the recipe show the merge that will actually be
// asked for. Whether it is needed is decided again when the branch's turn
// comes; this is the forecast.
func forecastAdmin(steps []Step, blocking []string, admin bool) ([]Step, []string) {
	if !admin {
		return steps, blocking
	}
	for index := range steps {
		if slices.Contains(blocking, steps[index].Branch) {
			steps[index].Admin = true
		}
	}
	return steps, nil
}

// protectedAfterRestack names the branches that will read blocked by the time
// their turn comes.
//
// Every branch above the first is force-pushed by its own restack, which
// restarts the required checks that were green when this was planned. On a
// protected repository that is not an edge case, it is every run, and saying it
// at the start is the difference between choosing --admin and discovering it
// once something has already merged.
func protectedAfterRestack(steps []Step, mergeability githubstack.Mergeability) []string {
	protects := false
	for _, state := range mergeability.States {
		if state.StateStatus == githubstack.StatusBlocked || state.StateStatus == githubstack.StatusBehind {
			protects = true
		}
	}
	if !protects {
		return nil
	}
	after := make([]string, 0, len(steps))
	for index, step := range steps {
		if index != 0 && step.Merges() {
			after = append(after, step.Branch)
		}
	}
	return after
}

func stateFor(mergeability githubstack.Mergeability, step githubstack.PathStep) githubstack.MergeState {
	if open := step.Resolution.Open; open != nil {
		return mergeability.States[open.Number]
	}
	return githubstack.MergeState{}
}

func allowedWays(allowed githubstack.Allowed) []repair.Step {
	ways := make([]repair.Step, 0, len(githubstack.Methods))
	for _, method := range githubstack.Methods {
		if allowed.Permits(method) {
			ways = append(ways, repair.Step{Command: "g2g land --method " + string(method), Effect: "use the method this repository allows"})
		}
	}
	if len(ways) == 0 {
		ways = append(ways, repair.Step{Effect: "allow a merge method in the repository's settings"})
	}
	return ways
}

// unpublished counts the commits on the branch, above its base, whose content
// the remote's version of it lacks.
func (s Service) unpublished(ctx context.Context, remote, tip, base string) (int, error) {
	absent, _, err := s.Git.Cherry(ctx, remote, tip, base)
	return len(absent), err
}

// intoTrunk aims every branch at the trunk. Landing merges each into the
// trunk in turn, so that is what each pull request's base has to be by the
// time its turn comes -- not the branch below it, which is where it correctly
// sits now and which will not exist by then.
func intoTrunk(trunk string, branches []string) map[string]string {
	aimed := make(map[string]string, len(branches))
	for _, branch := range branches {
		aimed[branch] = trunk
	}
	return aimed
}

package githubstack

// Resolution is the pull request that represents one branch, together with the
// facts a caller needs when no single open pull request exists.
//
// A branch's identity is its one open pull request. A long-lived stack
// accumulates closed and merged pull requests for a reused branch name, and
// those are history rather than ambiguity — treating any second match as
// ambiguous blocked branches whose only fault was having been submitted
// before. Only two or more simultaneously open pull requests are genuinely
// ambiguous, because then nothing distinguishes which one the stack means.
type Resolution struct {
	// Open is the single open pull request, or nil when there is not exactly one.
	Open *PullRequest
	// Latest is the highest-numbered pull request of any state. It reports the
	// closed or merged state of a branch that has no open pull request.
	Latest *PullRequest
	// OpenCount distinguishes "none open" from "too many open".
	OpenCount int
}

// Ambiguous reports the only case gt2gh refuses to interpret.
func (r Resolution) Ambiguous() bool { return r.OpenCount > 1 }

// Superseded reports a branch whose pull requests are all closed or merged.
// An ambiguous branch has open pull requests and is deliberately not included.
func (r Resolution) Superseded() bool { return r.OpenCount == 0 && r.Latest != nil }

// ResolveHeads applies the open-is-identity rule to every inspected branch.
func ResolveHeads(prs []PullRequest) map[string]Resolution {
	resolutions := make(map[string]Resolution, len(prs))
	for _, pr := range prs {
		resolution := resolutions[pr.Head]
		if resolution.Latest == nil || pr.Number > resolution.Latest.Number {
			latest := pr
			resolution.Latest = &latest
		}
		if pr.State == stateOpen {
			resolution.OpenCount++
			open := pr
			resolution.Open = &open
		}
		resolutions[pr.Head] = resolution
	}
	for head, resolution := range resolutions {
		if resolution.OpenCount != 1 {
			resolution.Open = nil
			resolutions[head] = resolution
		}
	}
	return resolutions
}

const stateOpen = "OPEN"

// PathStep is one branch on an ordered stack: the branch, the base it should
// sit on, and the pull request that represents it.
type PathStep struct {
	Branch       string
	ExpectedBase string
	Resolution   Resolution
}

// Along yields each branch of an ordered path with its expected base and
// resolved pull request.
//
// The base rolls: the bottom branch sits on the trunk and every branch above
// sits on its predecessor. link, sync and submit each derive that same chain
// before applying their own policy, and deriving it three times is what let a
// fourth caller drift into scanning for the first open pull request instead of
// going through the open-is-identity rule.
func Along(base string, branches []string, prs []PullRequest) func(func(PathStep) bool) {
	resolutions := ResolveHeads(prs)
	return func(yield func(PathStep) bool) {
		for _, branch := range branches {
			if !yield(PathStep{Branch: branch, ExpectedBase: base, Resolution: resolutions[branch]}) {
				return
			}
			base = branch
		}
	}
}

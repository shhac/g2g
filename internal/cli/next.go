package cli

import (
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/restack"
)

// replayNext follows a rewrite, pull's or restack's, from what it did. A
// branch whose work it found already in its base is forgotten first, because
// publishing would push a branch that has landed; prune also records what sat
// on it where it now sits. Otherwise a replay leaves the published branches
// behind their local ones, and push previews what publishing them would do. A
// rewrite that replayed nothing leaves nothing to follow.
func replayNext(plan restack.Plan) string {
	switch {
	case len(plan.Emptied()) != 0 && plan.Scope == graph.ScopeTrunk:
		return "g2g prune --scope trunk"
	case len(plan.Emptied()) != 0:
		return "g2g prune"
	case len(plan.Replaying()) != 0:
		return "g2g push"
	default:
		return ""
	}
}

package syncpoint

import localgit "github.com/shhac/g2g/internal/git"

// Level constructs agreements only where nonempty observed tips are exactly
// equal. Callers own reads, branch eligibility, and recording diagnostics.
func Level(command string, local, published map[string]string) map[string]localgit.SyncPoint {
	points := make(map[string]localgit.SyncPoint)
	for branch, tip := range local {
		if tip != "" && tip == published[branch] {
			points[branch] = localgit.SyncPoint{Tip: tip, Local: tip, Command: command}
		}
	}
	return points
}

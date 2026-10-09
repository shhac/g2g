package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/push"
	"github.com/shhac/g2g/internal/syncpoint"
)

// recentDrops is how long status keeps naming commits a pull or push dropped.
// Long enough to notice something missing the day after; the reflog that
// holds them keeps them reachable for about 30 days more.
const recentDrops = 7 * 24 * time.Hour

// recentHistory is how much of each branch's sync-point history the note
// looks through: enough for a week of pulls and pushes.
const recentHistory = 10

// noteRecentDrops names commits a pull or push took off a shown branch in the
// last week, from the sync points' own history, with the command that brings
// one back. Somebody who did not read the pull that dropped them finds them
// here. Read from local refs alone, as everything else status says.
func noteRecentDrops(ctx context.Context, view stackView, published push.Known, remote string, discovery graph.Discovery, now time.Time) stackView {
	reader, ok := published.Git.(syncpoint.HistoryReader)
	if !ok {
		return view
	}
	// A branch whose history could not be read says nothing; the rest still
	// do, as each did when it was asked on its own.
	histories, _ := reader.SyncHistories(ctx, remote, discovery.Branches)
	said := make([]string, 0)
	for _, branch := range discovery.Branches {
		points := histories[branch]
		said = append(said, recentDropEntries(branch, points[:min(len(points), recentHistory)], now)...)
	}
	if len(said) == 0 {
		return view
	}
	return view.note("Dropped recently: "+strings.Join(said, ", ")+" · "+runnable("git branch <name> <commit>")+" brings one back while the sync point's reflog keeps it.", severityNeutral)
}

// ago says how long since, in the unit a person would.
func ago(since time.Duration) string {
	switch {
	case since < time.Hour:
		return "just now"
	case since < 24*time.Hour:
		return count(int(since.Hours()), "hour", "hours") + " ago"
	default:
		return count(int(since.Hours()/24), "day", "days") + " ago"
	}
}

// recentDropEntries names each commit a branch's sync points say was dropped
// within the window, with the command that dropped it and when.
func recentDropEntries(branch string, points []localgit.SyncPoint, now time.Time) []string {
	said := make([]string, 0)
	for _, point := range points {
		if len(point.Dropped) == 0 || now.Sub(point.At) > recentDrops {
			continue
		}
		for _, commit := range point.Dropped {
			said = append(said, fmt.Sprintf("%s %s (g2g %s, %s)", branch, localgit.Short(commit), point.Command, ago(now.Sub(point.At))))
		}
	}
	return said
}

package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/push"
	"github.com/shhac/g2g/internal/syncpoint"
	"github.com/shhac/g2g/internal/words"
)

// recentDrops is how long status keeps naming commits a pull or push dropped.
// Long enough to notice something missing the day after; the reflog that
// holds them keeps them reachable for about 30 days more.
const recentDrops = 7 * 24 * time.Hour

// recentHistory is how much of each branch's sync-point history the note
// looks through: enough for a week of pulls and pushes.
const recentHistory = 10

// recentDropGroups is how many drops the note spells out, and recentDropNamed
// how many commits of each. A drop is one command taking commits off one
// branch. The note is a prompt to go and look, and a list of everything a busy
// week dropped was two hundred commits on one line, under which nothing else
// status said could be found.
const (
	recentDropGroups = 3
	recentDropNamed  = 3
)

// recentDrop is the commits one command took off one branch.
type recentDrop struct {
	branch, command string
	at              time.Time
	commits         []string
}

// noteRecentDrops names commits a pull or push took off a shown branch in the
// last week, from the sync points' own history, with the command that brings
// one back. Somebody who did not read the pull that dropped them finds them
// here. Read from local refs alone, as everything else status says.
//
// The newest few drops are named and the rest counted, with where the whole
// list is kept.
func noteRecentDrops(ctx context.Context, view stackView, published push.Known, remote string, discovery graph.Discovery, now time.Time) stackView {
	reader, ok := published.Git.(syncpoint.HistoryReader)
	if !ok {
		return view
	}
	// A branch whose history could not be read says nothing; the rest still
	// do, as each did when it was asked on its own.
	histories, _ := reader.SyncHistories(ctx, remote, discovery.Branches)
	drops := make([]recentDrop, 0)
	for _, branch := range discovery.Branches {
		points := histories[branch]
		drops = append(drops, recentDropsOf(branch, points[:min(len(points), recentHistory)], now)...)
	}
	if len(drops) == 0 {
		return view
	}
	return view.note(recentDropNote(drops, remote, now), severityNeutral)
}

// recentDropNote spells out the newest drops and counts the rest.
func recentDropNote(drops []recentDrop, remote string, now time.Time) string {
	slices.SortStableFunc(drops, func(a, b recentDrop) int { return b.at.Compare(a.at) })
	shown, rest := drops[:min(len(drops), recentDropGroups)], drops[min(len(drops), recentDropGroups):]
	said := make([]string, 0, len(shown))
	for _, drop := range shown {
		said = append(said, drop.says(now))
	}
	text := "Dropped recently: " + strings.Join(said, ", ")
	if len(rest) != 0 {
		commits, branches := 0, make(map[string]bool)
		for _, drop := range rest {
			commits += len(drop.commits)
			branches[drop.branch] = true
		}
		text += fmt.Sprintf(", and %d more %s on %s", commits, words.Pick(commits, "commit", "commits"), words.Count(len(branches), "branch", "branches"))
	}
	text += " · " + runnable("git branch <name> <commit>") + " brings one back while the sync point's reflog keeps it"
	abridged := len(rest) != 0 || slices.ContainsFunc(shown, func(drop recentDrop) bool { return len(drop.commits) > recentDropNamed })
	if abridged {
		text += ", and " + runnable("git reflog show refs/g2g/synced/"+remote+"/<branch>") + " lists every one"
	}
	return text + "."
}

// says names the first few commits of a drop and counts the rest.
func (d recentDrop) says(now time.Time) string {
	named := make([]string, 0, recentDropNamed)
	for _, commit := range d.commits[:min(len(d.commits), recentDropNamed)] {
		named = append(named, localgit.Short(commit))
	}
	commits := strings.Join(named, ", ")
	if more := len(d.commits) - len(named); more > 0 {
		commits += " and " + words.Count(more, "other", "others")
	}
	return fmt.Sprintf("%s %s (g2g %s, %s)", d.branch, commits, d.command, ago(now.Sub(d.at)))
}

// ago says how long since, in the unit a person would.
func ago(since time.Duration) string {
	switch {
	case since < time.Hour:
		return "just now"
	case since < 24*time.Hour:
		return words.Count(int(since.Hours()), "hour", "hours") + " ago"
	default:
		return words.Count(int(since.Hours()/24), "day", "days") + " ago"
	}
}

// recentDropsOf is each drop a branch's sync points record within the window.
// A commit an earlier drop already named is not named again: a branch pushed
// twice after a rewrite records the same commits both times.
func recentDropsOf(branch string, points []localgit.SyncPoint, now time.Time) []recentDrop {
	drops := make([]recentDrop, 0)
	seen := make(map[string]bool)
	for _, point := range slices.Backward(points) {
		if len(point.Dropped) == 0 || now.Sub(point.At) > recentDrops {
			continue
		}
		drop := recentDrop{branch: branch, command: point.Command, at: point.At}
		for _, commit := range point.Dropped {
			if !seen[commit] {
				seen[commit] = true
				drop.commits = append(drop.commits, commit)
			}
		}
		if len(drop.commits) != 0 {
			drops = append(drops, drop)
		}
	}
	return drops
}

package cli

import (
	"strings"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/syncpoint"
)

// dropList names each commit by branch, short id and subject; a moved one by
// the branch it left and the branch it is in now. Pull, push and restack name
// a commit leaving a branch the same way, because a person looking for one
// after the fact should not have to know which command dropped it.
func dropList(subjects map[string]string, drops []syncpoint.Drop) string {
	said := make([]string, 0, len(drops))
	for _, drop := range drops {
		entry := drop.Branch + " " + localgit.Short(drop.Commit)
		if subject := subjects[drop.Commit]; subject != "" {
			entry += " " + subject
		}
		if drop.To != "" {
			entry += " (" + drop.Branch + " → " + drop.To + ")"
		}
		said = append(said, entry)
	}
	return strings.Join(said, ", ")
}

// commitRecords are the same commits for a machine, one record each, tagged
// with what happens to them.
func commitRecords(kind string, subjects map[string]string, drops []syncpoint.Drop) []stackCommit {
	records := make([]stackCommit, 0, len(drops))
	for _, drop := range drops {
		records = append(records, stackCommit{Branch: drop.Branch, Commit: drop.Commit, Subject: subjects[drop.Commit], Kind: kind, To: drop.To})
	}
	return records
}

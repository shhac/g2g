package git

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Naming commits: what a preview says about each one it lists, and what a
// replay carries over from it, each asked of every commit in one read.

// Commit is one commit named by its full object id, with its subject so a
// preview can say what it is rather than only that it exists.
type Commit struct {
	ID      string
	Subject string
}

// Short is a commit id at the length a person reads and pastes back. One rule,
// because the ids a preview lists are the ids --keep is asked for: a preview
// printing one length and a refusal suggesting another would name commits the
// reader could not match.
func Short(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

// Subjects names each commit by its subject, keyed by its full id, in one
// read. A preview that drops, moves or keeps commits lists every one of them
// by what it is.
func (c Client) Subjects(ctx context.Context, ids []string) (map[string]string, error) {
	commits, err := c.Describe(ctx, ids)
	if err != nil {
		return nil, err
	}
	named := make(map[string]string, len(commits))
	for _, commit := range commits {
		named[commit.ID] = commit.Subject
	}
	return named, nil
}

// Carried is what a replay carries over from a commit -- who wrote it, when,
// and its subject -- and when the commit itself was made. git rebase, git
// replay and cherry-pick keep the first three and change only the patch and
// the committer, so two commits that agree on Key are one commit replayed,
// and the replay is the one committed no earlier.
type Carried struct {
	Key       string
	Committed int64
}

// Carried reads what a replay carries over from each commit, keyed by its
// full id, in one read.
func (c Client) Carried(ctx context.Context, ids []string) (map[string]Carried, error) {
	lines, err := c.logEach(ctx, "%H%x00%ct%x00%an%x00%ae%x00%at%x00%s", ids)
	if err != nil {
		return nil, err
	}
	carried := make(map[string]Carried, len(ids))
	for _, line := range lines {
		id, rest, _ := strings.Cut(line, "\x00")
		committed, key, _ := strings.Cut(rest, "\x00")
		at, err := strconv.ParseInt(committed, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse git log committer date %q: %w", committed, err)
		}
		carried[id] = Carried{Key: key, Committed: at}
	}
	return carried, nil
}

// Describe names each commit with its subject, in the order given, in one
// read. A preview that drops, moves or keeps commits lists every one of them
// by what it is, because once a ref has moved nothing here will name them
// again.
func (c Client) Describe(ctx context.Context, ids []string) ([]Commit, error) {
	lines, err := c.logEach(ctx, "%H%x09%s", ids)
	if err != nil {
		return nil, err
	}
	commits := make([]Commit, 0, len(lines))
	for _, line := range lines {
		id, subject, _ := strings.Cut(line, "\t")
		commits = append(commits, Commit{ID: id, Subject: subject})
	}
	return commits, nil
}

// logEach is one line of format for each of ids, in the order given, in one
// git log. Every id is checked before any reaches Git, and no ids ask nothing.
func (c Client) logEach(ctx context.Context, format string, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	for _, id := range ids {
		if err := safeRef(id); err != nil {
			return nil, err
		}
	}
	output, err := c.run(ctx, append([]string{"log", "--no-walk=unsorted", "--format=" + format}, ids...)...)
	if err != nil {
		return nil, err
	}
	return outputLines(output), nil
}

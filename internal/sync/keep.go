package sync

import (
	"context"
	"fmt"
	"slices"
	"strings"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/repair"
)

// resolveKeep turns the commits a caller named into full ids, refusing one
// that names nothing.
func (s Service) resolveKeep(ctx context.Context, keep []string) ([]string, error) {
	resolved := make([]string, 0, len(keep))
	for _, commit := range keep {
		id, err := s.Git.Resolve(ctx, commit+"^{commit}")
		if err != nil {
			return nil, fmt.Errorf("--keep %s names no commit here", commit)
		}
		if !slices.Contains(resolved, id) {
			resolved = append(resolved, id)
		}
	}
	return resolved, nil
}

// unkeptRefusal refuses a --keep naming a commit this pull would not drop.
// Silently ignoring it would let a mistyped id read as kept.
func unkeptRefusal(plan Plan) (repair.Note, bool) {
	unkept := make([]string, 0)
	for _, commit := range plan.Keep {
		if !slices.ContainsFunc(plan.Kept, func(drop Drop) bool { return drop.Commit == commit }) {
			unkept = append(unkept, localgit.Short(commit))
		}
	}
	if len(unkept) == 0 {
		return repair.Note{}, false
	}
	return repair.Note{
		Reason: fmt.Sprintf("--keep %s %s not a commit this pull would drop", strings.Join(unkept, ", "), pick(len(unkept), "is", "are")),
		Ways:   []repair.Step{{Effect: "name only commits the preview lists as dropped"}},
	}, true
}

// keepCommand is the pull that keeps these commits, for the selection and
// remote this one was asked about.
func (c collecting) keepCommand(commits []string) string {
	command := c.command
	for _, commit := range commits {
		command += " --keep " + localgit.Short(commit)
	}
	return command
}

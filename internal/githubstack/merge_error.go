package githubstack

import (
	"errors"
	"strings"
)

// BaseModified identifies GitHub's explicit refusal of a merge whose base
// changed during the request. Other failures may have applied the merge and
// must not be retried as though they were this known refusal.
func BaseModified(err error) bool {
	var command *CommandError
	return errors.As(err, &command) && strings.HasPrefix(command.Command, "gh pr merge ") &&
		strings.Contains(command.Output, "GraphQL: Base branch was modified. Review and try the merge again. (mergePullRequest)")
}

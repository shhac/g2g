package githubstack

import (
	"errors"
	"fmt"
	"testing"
)

func TestBaseModifiedRecognisesOnlyTheExplicitMergeRefusal(t *testing.T) {
	message := "GraphQL: Base branch was modified. Review and try the merge again. (mergePullRequest)"
	for name, test := range map[string]struct {
		err  error
		want bool
	}{
		"merge":             {&CommandError{Command: "gh pr merge 41 --squash", Output: message}, true},
		"wrapped":           {fmt.Errorf("synthetic wrapper: %w", &CommandError{Command: "gh pr merge 41 --squash", Output: message}), true},
		"other command":     {&CommandError{Command: "gh pr edit 41", Output: message}, false},
		"ambiguous failure": {&CommandError{Command: "gh pr merge 41 --squash", Output: "synthetic connection lost"}, false},
		"ordinary error":    {errors.New(message), false},
		"success":           {nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := BaseModified(test.err); got != test.want {
				t.Fatalf("BaseModified = %v, want %v", got, test.want)
			}
		})
	}
}

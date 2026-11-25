package restack

import (
	"context"
	"errors"
	"testing"

	localgit "github.com/shhac/g2g/internal/git"
)

type statusGit struct {
	*fakeGit
	status error
}

func (g statusGit) Clean(context.Context) error { return g.status }

// Uncommitted work is a refusal on the plan, a status Git could not give is
// an error, and a plan that meets no working tree asks nothing at all. A read
// the budget cut off used to be reported as changes to commit.
func TestUncommittedTellsChangesFromAStatusThatFailed(t *testing.T) {
	unreadable := errors.New("synthetic status failure")
	moving := Plan{Here: "synthetic-a"}
	for _, tc := range []struct {
		name    string
		plan    Plan
		status  error
		refused bool
		err     error
	}{
		{name: "clean", plan: moving},
		{name: "changes on the branch that moves", plan: moving, status: localgit.ErrNotClean, refused: true},
		{name: "status failed", plan: moving, status: unreadable, err: unreadable},
		{name: "changes the replay never meets", plan: Plan{Clean: true, Steps: []Step{{Branch: "synthetic-b"}}}, status: localgit.ErrNotClean},
		{name: "a replay that needs a rebase", plan: Plan{Steps: []Step{{Branch: "synthetic-b"}}}, status: localgit.ErrNotClean, refused: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Service{Git: statusGit{status: tc.status}}.Uncommitted(context.Background(), tc.plan)
			if !errors.Is(err, tc.err) || (tc.err == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, tc.err)
			}
			if (got.Blocked() != "") != tc.refused {
				t.Errorf("blocked = %q, want refused %t", got.Blocked(), tc.refused)
			}
		})
	}
}

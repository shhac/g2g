package push

import (
	"context"
	"errors"
	"testing"
)

type conflictingGit struct {
	comparingGit
	tips      map[string]string
	conflicts map[string]bool
	readErr   error
}

func (g *conflictingGit) KnownTips(context.Context, string, []string) (map[string]string, error) {
	panic("Known must read tips and conflicts together")
}

func (g *conflictingGit) KnownTipsWithConflicts(context.Context, string, []string) (map[string]string, map[string]bool, error) {
	return g.tips, g.conflicts, g.readErr
}

func TestKnownDoesNotInferPublicationFromConflictingRecordsOrBases(t *testing.T) {
	git := &conflictingGit{
		tips:      map[string]string{"synthetic-parent": "old-parent", "synthetic-child": "old-child"},
		conflicts: map[string]bool{"synthetic-parent": true},
	}
	// No resolvable revisions: trying to compare either uncertain branch
	// would fail, rather than yield an honest uncertainty result.
	publishing, _, err := (Known{Git: git}).Compare(context.Background(), "origin", []string{"synthetic-parent", "synthetic-child"}, func(branch string) (string, string) {
		if branch == "synthetic-child" {
			return "synthetic-parent", "synthetic-main"
		}
		return "synthetic-main", "synthetic-main"
	})
	if err != nil || len(publishing) != 2 {
		t.Fatalf("Compare() = %v, %v", publishing, err)
	}
	for branch, publication := range publishing {
		if publication.Standing != Conflicting || publication.UpToDate() || publication.Unpublished() {
			t.Errorf("%s: uncertain records produced %+v", branch, publication)
		}
	}
}

func TestKnownReturnsReadAndComparisonErrorsAlongsideConflictingRecords(t *testing.T) {
	for _, readFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "comparison", true: "read"}[readFailure], func(t *testing.T) {
			git := &conflictingGit{tips: map[string]string{"synthetic-bad": "bad"}, conflicts: map[string]bool{"synthetic-uncertain": true}}
			if readFailure {
				git.readErr = errors.New("synthetic read failure")
			}
			_, _, err := (Known{Git: git}).Compare(context.Background(), "origin", []string{"synthetic-bad", "synthetic-uncertain"}, func(string) (string, string) {
				return "synthetic-main", "synthetic-main"
			})
			if err == nil {
				t.Fatal("failure was hidden by conflicting records")
			}
		})
	}
}

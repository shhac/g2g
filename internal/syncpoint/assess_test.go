package syncpoint

import (
	"context"
	"testing"

	localgit "github.com/shhac/g2g/internal/git"
)

// Any content read through the embedded nil interface panics: unavailable
// published objects must not be asked about to explain agreement evidence.
type evidenceGit struct {
	Assessor
	points    map[string][]localgit.SyncPoint
	describes bool
}

func (g evidenceGit) SyncHistories(context.Context, string, []string) (map[string][]localgit.SyncPoint, error) {
	return g.points, nil
}
func (g evidenceGit) Describes(context.Context, string, localgit.SyncPoint) (bool, error) {
	return g.describes, nil
}

func TestAssessAgreementWithoutPublishedObjects(t *testing.T) {
	for _, test := range []struct {
		name                            string
		exists, describes, wantUnsynced bool
	}{
		{"missing", false, false, true},
		{"record belongs to earlier branch", true, false, true},
		{"usable", true, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			git := evidenceGit{describes: test.describes, points: map[string][]localgit.SyncPoint{}}
			if test.exists {
				git.points["synthetic-top"] = []localgit.SyncPoint{{Tip: "synthetic-published", Local: "synthetic-local"}}
			}
			latest, err := LoadLatest(context.Background(), git, "origin", []string{"synthetic-top"})
			if err != nil {
				t.Fatal(err)
			}
			changes, err := Assess(context.Background(), git, latest, "synthetic-top", Branch{})
			if err != nil || changes.Unsynced != test.wantUnsynced || changes.Dropped() {
				t.Fatalf("Assess()=%+v, %v", changes, err)
			}
		})
	}
}

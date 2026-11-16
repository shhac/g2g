package cli

import (
	"testing"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/landed"
)

func TestSupersededHistory(t *testing.T) {
	for _, tc := range []struct {
		name, state, base, trunk string
		evidence                 landed.Evidence
		want                     string
	}{
		{"closed", "CLOSED", "synthetic-base", "synthetic-trunk", landed.Unknown, "closed"},
		{"landed", "MERGED", "synthetic-base", "synthetic-trunk", landed.Present, "merged · landed in synthetic-trunk"},
		{"feature absent", "MERGED", "synthetic-base", "synthetic-trunk", landed.Absent, "merged into synthetic-base, not landed"},
		{"feature unknown", "MERGED", "synthetic-base", "synthetic-trunk", landed.Unknown, "merged into synthetic-base, landing unconfirmed"},
		{"trunk absent", "MERGED", "synthetic-trunk", "synthetic-trunk", landed.Absent, "merged into synthetic-trunk; refresh synthetic-trunk and reassess"},
		{"trunk unknown", "MERGED", "synthetic-trunk", "synthetic-trunk", landed.Unknown, "merged into synthetic-trunk; refresh synthetic-trunk and reassess"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := supersededHistory(githubstack.PullRequest{State: tc.state, Base: tc.base}, tc.trunk, tc.evidence); got != tc.want {
				t.Errorf("history=%q; want %q", got, tc.want)
			}
		})
	}
}
